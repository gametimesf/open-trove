// Package slackapp is Trove's Slack adapter: direct-message notifications and
// link previews over Socket Mode. See docs/slack.md.
package slackapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"

	"github.com/gametimesf/open-trove/comments"
	"github.com/gametimesf/open-trove/notify"
)

// API is the subset of the Slack Web API this package uses.
type API interface {
	GetUserByEmailContext(ctx context.Context, email string) (*slack.User, error)
	PostMessageContext(ctx context.Context, channelID string, options ...slack.MsgOption) (string, string, error)
	GetConversationInfoContext(ctx context.Context, input *slack.GetConversationInfoInput) (*slack.Channel, error)
	UnfurlMessageContext(ctx context.Context, channelID, timestamp string, unfurls map[string]slack.Attachment, options ...slack.MsgOption) (string, string, string, error)
	GetUsersContext(ctx context.Context, options ...slack.GetUsersOption) ([]slack.User, error)
	GetUsersInConversationContext(ctx context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error)
}

const (
	userCacheTTL    = time.Hour
	missingCacheTTL = 10 * time.Minute
)

// Slack error codes meaning a person can't be messaged; retrying won't help.
var undeliverableCodes = map[string]bool{
	"users_not_found":  true,
	"user_not_found":   true,
	"user_disabled":    true,
	"account_inactive": true,
	"cannot_dm_bot":    true,
}

type cachedUser struct {
	id      string // empty: no Slack user for this email
	expires time.Time
}

// Notifier sends notifications as direct messages from the Trove app. It
// implements notify.Notifier.
type Notifier struct {
	api   API
	now   func() time.Time
	mu    sync.Mutex
	users map[string]cachedUser
}

// NewNotifier returns a Notifier using api.
func NewNotifier(api API) *Notifier {
	return &Notifier{api: api, now: time.Now, users: make(map[string]cachedUser)}
}

// Notify sends n to its recipient's Slack DM.
func (s *Notifier) Notify(ctx context.Context, n notify.Notification) error {
	userID, err := s.userID(ctx, n.RecipientEmail)
	if err != nil {
		return err
	}
	if _, _, err := s.api.PostMessageContext(ctx, userID,
		slack.MsgOptionText(plainSummary(n), false),
		slack.MsgOptionBlocks(messageBlocks(n)...),
		slack.MsgOptionDisableLinkUnfurl(),
	); err != nil {
		return classify(fmt.Errorf("posting message: %w", err))
	}
	return nil
}

func (s *Notifier) userID(ctx context.Context, email string) (string, error) {
	now := s.now()
	s.mu.Lock()
	cached, ok := s.users[email]
	s.mu.Unlock()
	if ok && now.Before(cached.expires) {
		if cached.id == "" {
			return "", fmt.Errorf("no slack user for recipient: %w", notify.ErrUndeliverable)
		}
		return cached.id, nil
	}

	user, err := s.api.GetUserByEmailContext(ctx, email)
	if err != nil {
		err = classify(fmt.Errorf("looking up slack user: %w", err))
		if errors.Is(err, notify.ErrUndeliverable) {
			s.store(email, cachedUser{expires: now.Add(missingCacheTTL)})
		}
		return "", err
	}
	if user.Deleted || user.IsBot {
		s.store(email, cachedUser{expires: now.Add(missingCacheTTL)})
		return "", fmt.Errorf("slack user is deactivated or a bot: %w", notify.ErrUndeliverable)
	}
	s.store(email, cachedUser{id: user.ID, expires: now.Add(userCacheTTL)})
	return user.ID, nil
}

func (s *Notifier) store(email string, u cachedUser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[email] = u
}

// classify marks Slack errors that retrying can't fix as undeliverable.
func classify(err error) error {
	var resp slack.SlackErrorResponse
	if errors.As(err, &resp) && undeliverableCodes[resp.Err] {
		return fmt.Errorf("%w: %w", notify.ErrUndeliverable, err)
	}
	return err
}

var verbs = map[comments.ActivityKind]string{
	comments.ActivityThreadCreated:  "commented on",
	comments.ActivityReplyCreated:   "replied to a thread on",
	comments.ActivityThreadResolved: "resolved your thread on",
	comments.ActivityThreadReopened: "reopened your thread on",
}

// messageBlocks renders the notification: a summary line linking to the
// artifact, then the quoted excerpt when there is one.
func messageBlocks(n notify.Notification) []slack.Block {
	blocks := []slack.Block{slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, summary(n), false, false), nil, nil)}
	if n.Excerpt != "" {
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, "> "+escape(n.Excerpt), false, false), nil, nil))
	}
	return blocks
}

func summary(n notify.Notification) string {
	return fmt.Sprintf("*%s* %s <%s|%s>", escape(n.ActorEmail), verbs[n.Kind], n.ArtifactURL, linkText(n.ArtifactTitle))
}

// plainSummary is the notification fallback text (shown in notifications and
// clients without block support). Slack parses it as mrkdwn, so it's escaped.
func plainSummary(n notify.Notification) string {
	return escape(fmt.Sprintf("%s %s %s", n.ActorEmail, verbs[n.Kind], n.ArtifactTitle))
}

// escape applies Slack's mrkdwn escaping for user-supplied text.
func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// linkText escapes text used inside <url|text>, where "|" would end the label.
func linkText(s string) string {
	if s == "" {
		s = "an artifact"
	}
	return strings.ReplaceAll(escape(s), "|", "¦")
}
