package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gametimesf/open-trove/comments"
	"github.com/gametimesf/open-trove/internal/config"
	"github.com/gametimesf/open-trove/notify"
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
		RecipientIsOwner: true, ArtifactTitle: "q3-plan.html", ArtifactURL: "https://trove.example.com/q3-plan",
		Excerpt: "Can we tighten the timeline?",
	}
	if sent[0] != want {
		t.Fatalf("notification = %+v\nwant %+v", sent[0], want)
	}
}

func TestArtifactInfoPreview(t *testing.T) {
	store := fake.NewStore()
	ctx := t.Context()
	if err := store.Put(ctx, "doc", strings.NewReader("x"), storage.PutOptions{ContentType: "text/plain", Filename: "doc.txt", OwnerEmail: "owner@example.com"}); err != nil {
		t.Fatal(err)
	}
	resolvedAt := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	for _, c := range []comments.Comment{
		{ID: "a", ThreadID: "a", Slug: "doc", AuthorEmail: "x@example.com"},
		{ID: "b", ThreadID: "b", Slug: "doc", AuthorEmail: "x@example.com", ResolvedAt: &resolvedAt},
		{ID: "c", ThreadID: "a", ParentID: "a", Slug: "doc", AuthorEmail: "y@example.com"},
	} {
		if err := store.CreateComment(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	info := artifactInfo{store: store}

	p, err := info.Preview(ctx, "doc")
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if p.Title != "doc.txt" || p.ContentType != "text/plain" || p.OwnerEmail != "owner@example.com" || p.OpenThreads != 1 {
		t.Fatalf("preview = %+v", p)
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
