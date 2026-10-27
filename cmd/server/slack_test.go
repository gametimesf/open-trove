package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gametimesf/open-trove/comments"
	"github.com/gametimesf/open-trove/internal/config"
	"github.com/gametimesf/open-trove/notify"
	"github.com/gametimesf/open-trove/preview"
	"github.com/gametimesf/open-trove/slackapp"
	"github.com/gametimesf/open-trove/storage"
	"github.com/gametimesf/open-trove/storage/fake"
	"github.com/labstack/echo/v4"
)

type capturingNotifier struct {
	mu   sync.Mutex
	sent []notify.Notification
}

func (c *capturingNotifier) Notify(_ context.Context, n notify.Notification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, n)
	return nil
}

func (c *capturingNotifier) snapshot() []notify.Notification {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]notify.Notification(nil), c.sent...)
}

func TestCommentOverHTTPNotifiesOwner(t *testing.T) {
	store := fake.NewStore()
	if err := store.Put(t.Context(), "q3-plan", strings.NewReader("<p>plan</p>"), storage.PutOptions{ContentType: "text/html", Filename: "q3-plan.html", OwnerEmail: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	notifier := &capturingNotifier{}
	dispatcher := notify.NewDispatcher(notifier, artifactInfo{store: store}, store, "https://trove.example.com")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go dispatcher.Run(ctx)

	srv, _ := newTestServer()
	srv.store = store
	srv.comments = comments.NewService(store, comments.WithObserver(dispatcher))
	e := newTestEcho(srv)

	req := httptest.NewRequest(http.MethodPost, "/api/artifacts/q3-plan/comments", strings.NewReader(`{"body":"Can we tighten the timeline?","anchor":{"type":"file"}}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(troveUserEmailHeader, "reviewer@example.com")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment status = %d: %s", rec.Code, rec.Body.String())
	}
	var created comments.Comment
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(notifier.snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	sent := notifier.snapshot()
	if len(sent) != 1 {
		t.Fatalf("notifications = %+v, want 1", sent)
	}
	want := notify.Notification{
		Kind: comments.ActivityThreadCreated, RecipientEmail: "owner@example.com", ActorEmail: "reviewer@example.com",
		RecipientIsOwner: true, ArtifactTitle: "q3-plan.html", ArtifactURL: "https://trove.example.com/q3-plan?comment=" + created.ThreadID,
		Excerpt: "Can we tighten the timeline?",
	}
	if sent[0] != want {
		t.Fatalf("notification = %+v\nwant %+v", sent[0], want)
	}
}

func TestArtifactInfoPreview(t *testing.T) {
	store := fake.NewStore()
	ctx := t.Context()
	put := func(slug, contentType, filename, body string) {
		t.Helper()
		if err := store.Put(ctx, slug, strings.NewReader(body), storage.PutOptions{ContentType: contentType, Filename: filename, OwnerEmail: "owner@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	put("doc", "text/markdown; charset=utf-8", "doc.md", "# Launch plan\n\nShip pricing and inventory together.\n\n## Risks\n")
	put("secret", "text/markdown; charset=utf-8", "secret.md", "# Payroll\n\nSalaries by person.\n")
	if err := store.Flag("secret"); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSiteFile(ctx, "site", "index.html", strings.NewReader("<title>Handbook</title><h2>Setup</h2>"), "text/html"); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSiteManifest(ctx, "site", &storage.SiteManifest{Entry: "index.html", FileCount: 3, OwnerEmail: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	card := preview.Card{Image: []byte("png"), ContentType: "image/png", Width: 1200, Height: 630, SHA256: strings.Repeat("c", 64)}
	for _, slug := range []string{"doc", "secret"} {
		if err := store.PutCard(ctx, slug, card); err != nil {
			t.Fatal(err)
		}
	}
	older := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	for _, c := range []comments.Comment{
		{ID: "a", ThreadID: "a", Slug: "doc", AuthorEmail: "x@example.com", Body: "older open thread", CreatedAt: older},
		{ID: "b", ThreadID: "b", Slug: "doc", AuthorEmail: "x@example.com", Body: "resolved", CreatedAt: newer, ResolvedAt: &newer},
		{ID: "c", ThreadID: "a", ParentID: "a", Slug: "doc", AuthorEmail: "y@example.com", Body: "a reply", CreatedAt: newer},
		{ID: "d", ThreadID: "d", Slug: "doc", AuthorEmail: "z@example.com", Body: "newest open thread", CreatedAt: newer},
	} {
		if err := store.CreateComment(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	info := artifactInfo{store: store, baseURL: "https://trove.example.com"}

	tests := []struct {
		slug string
		want slackapp.Preview
	}{
		{
			slug: "doc",
			want: slackapp.Preview{
				Summary: preview.Summary{
					Kind: preview.KindMarkdown, Title: "Launch plan", Description: "Ship pricing and inventory together.",
					Sections: []string{"Risks"}, Facts: []string{"1 min read"},
				},
				OwnerEmail: "owner@example.com", OpenThreads: 2,
				LatestComment: &slackapp.CommentExcerpt{AuthorEmail: "z@example.com", Body: "newest open thread", URL: "https://trove.example.com/doc?comment=d"},
				Card:          &card,
			},
		},
		{
			slug: "secret",
			want: slackapp.Preview{Summary: preview.Summary{Kind: preview.KindMarkdown, Title: "secret.md"}, OwnerEmail: "owner@example.com", HiddenReason: slackapp.HiddenFlagged},
		},
		{
			slug: "site",
			want: slackapp.Preview{Summary: preview.Summary{Kind: preview.KindSite, Title: "Handbook", Sections: []string{"Setup"}, Facts: []string{"3 files"}}, OwnerEmail: "owner@example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			got, err := info.Preview(ctx, tt.slug)
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %#v\nwant %#v", got, tt.want)
			}
		})
	}

	a, err := info.Artifact(ctx, "doc")
	if err != nil || a.Title != "Launch plan" || a.OwnerEmail != "owner@example.com" {
		t.Fatalf("Artifact = %+v, %v", a, err)
	}
	for _, slug := range []string{"_prompt", "Bad Slug", "missing"} {
		if _, err := info.Preview(ctx, slug); err == nil {
			t.Errorf("Preview(%q) succeeded, want error", slug)
		}
	}
}

func TestStartSlackDisabled(t *testing.T) {
	opts, err := startSlack(context.Background(), config.Slack{}, "https://trove.example.com", fake.NewStore())
	if err != nil || opts != nil {
		t.Fatalf("startSlack(disabled) = %v, %v; want nil, nil", opts, err)
	}
}
