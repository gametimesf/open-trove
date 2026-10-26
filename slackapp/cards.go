package slackapp

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/slack-go/slack"

	"github.com/gametimesf/open-trove/preview"
)

// cardFailureTTL is how long a failed upload is remembered before retrying,
// so a missing scope or outage doesn't cost an API call on every unfurl.
const cardFailureTTL = 10 * time.Minute

// cardUploader uploads preview card images to Slack once per image content and
// remembers the resulting file IDs. Files are uploaded without sharing them to
// any conversation; unfurls reference them by ID.
type cardUploader struct {
	api API
	now func() time.Time

	mu       sync.Mutex
	files    map[string]string    // card SHA256 → Slack file ID
	failures map[string]time.Time // card SHA256 → last failed upload
}

func newCardUploader(api API) *cardUploader {
	return &cardUploader{api: api, now: time.Now, files: make(map[string]string), failures: make(map[string]time.Time)}
}

// fileID returns the Slack file ID for a card, uploading it if needed.
func (u *cardUploader) fileID(ctx context.Context, card *preview.Card) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if id, ok := u.files[card.SHA256]; ok {
		return id, nil
	}
	if at, ok := u.failures[card.SHA256]; ok && u.now().Sub(at) < cardFailureTTL {
		return "", fmt.Errorf("recent upload failure for card %s", card.SHA256)
	}
	file, err := u.api.UploadFileContext(ctx, slack.UploadFileParameters{
		Reader:   bytes.NewReader(card.Image),
		FileSize: len(card.Image),
		Filename: "trove-preview-" + card.SHA256[:12] + card.Extension(),
		Title:    "Trove preview",
		AltTxt:   card.Alt,
	})
	if err != nil {
		u.failures[card.SHA256] = u.now()
		return "", fmt.Errorf("uploading preview card: %w", err)
	}
	delete(u.failures, card.SHA256)
	u.files[card.SHA256] = file.ID
	return file.ID, nil
}

// reject drops a file Slack refused to show in an unfurl and backs off before
// uploading that card again.
func (u *cardUploader) reject(card *preview.Card) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.files, card.SHA256)
	u.failures[card.SHA256] = u.now()
}
