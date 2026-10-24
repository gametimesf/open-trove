package notify

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gametimesf/open-trove/comments"
)

const (
	owner    = "owner@example.com"
	author   = "author@example.com"
	reviewer = "reviewer@example.com"
	other    = "other@example.com"
)

var deletedAt = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

func thread() []comments.Comment {
	return []comments.Comment{
		{ID: "t1", ThreadID: "t1", AuthorEmail: author},
		{ID: "r1", ThreadID: "t1", ParentID: "t1", AuthorEmail: reviewer},
		{ID: "r2", ThreadID: "t1", ParentID: "t1", AuthorEmail: other, DeletedAt: &deletedAt},
		{ID: "t2", ThreadID: "t2", AuthorEmail: "elsewhere@example.com"},
	}
}

func TestRecipients(t *testing.T) {
	tests := []struct {
		name     string
		activity comments.Activity
		owner    string
		want     []string
	}{
		{
			name:     "new thread notifies owner",
			activity: comments.Activity{Kind: comments.ActivityThreadCreated, ActorEmail: author, Comment: comments.Comment{ThreadID: "t1"}},
			owner:    owner, want: []string{owner},
		},
		{
			name:     "owner commenting on own artifact notifies nobody",
			activity: comments.Activity{Kind: comments.ActivityThreadCreated, ActorEmail: owner, Comment: comments.Comment{ThreadID: "t1"}},
			owner:    owner, want: []string{},
		},
		{
			name:     "reply notifies owner and live participants except actor",
			activity: comments.Activity{Kind: comments.ActivityReplyCreated, ActorEmail: reviewer, Comment: comments.Comment{ThreadID: "t1"}},
			owner:    owner, want: []string{owner, author},
		},
		{
			name:     "unknown owner still reaches participants",
			activity: comments.Activity{Kind: comments.ActivityReplyCreated, ActorEmail: author, Comment: comments.Comment{ThreadID: "t1"}},
			owner:    "", want: []string{reviewer},
		},
		{
			name:     "owner who is also participant is notified once",
			activity: comments.Activity{Kind: comments.ActivityReplyCreated, ActorEmail: reviewer, Comment: comments.Comment{ThreadID: "t1"}},
			owner:    "AUTHOR@example.com", want: []string{author},
		},
		{
			name:     "resolution notifies thread author",
			activity: comments.Activity{Kind: comments.ActivityThreadResolved, ActorEmail: reviewer, Comment: comments.Comment{ThreadID: "t1", AuthorEmail: author}},
			owner:    owner, want: []string{author},
		},
		{
			name:     "author reopening own thread notifies nobody",
			activity: comments.Activity{Kind: comments.ActivityThreadReopened, ActorEmail: author, Comment: comments.Comment{ThreadID: "t1", AuthorEmail: author}},
			owner:    owner, want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Recipients(tt.activity, tt.owner, thread())
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Recipients() = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeArtifacts struct {
	artifact Artifact
	err      error
}

func (f fakeArtifacts) Artifact(context.Context, string) (Artifact, error) { return f.artifact, f.err }

type fakeLister struct{ comments []comments.Comment }

func (f fakeLister) ListComments(context.Context, string) ([]comments.Comment, error) {
	return f.comments, nil
}

// scriptedNotifier returns queued errors per recipient, then succeeds. It is
// safe to read from the test while a dispatcher goroutine delivers.
type scriptedNotifier struct {
	mu    sync.Mutex
	errs  map[string][]error
	calls map[string]int
	sent  []Notification
}

func (s *scriptedNotifier) sentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func (s *scriptedNotifier) Notify(_ context.Context, n Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[n.RecipientEmail]++
	if queued := s.errs[n.RecipientEmail]; len(queued) > 0 {
		s.errs[n.RecipientEmail] = queued[1:]
		return queued[0]
	}
	s.sent = append(s.sent, n)
	return nil
}

func newTestDispatcher(n Notifier, artifacts ArtifactLookup) *Dispatcher {
	d := NewDispatcher(n, artifacts, fakeLister{comments: thread()}, "https://trove.example.com/")
	d.sleep = func(context.Context, time.Duration) error { return nil }
	return d
}

func TestDispatcherDeliversReply(t *testing.T) {
	n := &scriptedNotifier{}
	d := newTestDispatcher(n, fakeArtifacts{artifact: Artifact{Title: "Q3 report", OwnerEmail: owner}})
	d.handle(context.Background(), comments.Activity{
		Kind: comments.ActivityReplyCreated, Slug: "q3-report", ActorEmail: reviewer,
		Comment: comments.Comment{ThreadID: "t1", Body: "  Looks\ngood  ", Anchor: comments.Anchor{Resource: "pages/a b.html"}},
	})
	if len(n.sent) != 2 {
		t.Fatalf("sent %d notifications, want 2: %+v", len(n.sent), n.sent)
	}
	first := n.sent[0]
	want := Notification{
		Kind: comments.ActivityReplyCreated, RecipientEmail: owner, ActorEmail: reviewer, RecipientIsOwner: true,
		ArtifactTitle: "Q3 report", ArtifactURL: "https://trove.example.com/q3-report?page=pages%2Fa+b.html", Excerpt: "Looks good",
	}
	if first != want {
		t.Fatalf("notification = %+v\nwant %+v", first, want)
	}
	if n.sent[1].RecipientEmail != author || n.sent[1].RecipientIsOwner {
		t.Fatalf("second notification = %+v", n.sent[1])
	}
}

func TestDispatcherRetries(t *testing.T) {
	transient := errors.New("rate limited")
	tests := []struct {
		name      string
		errs      []error
		wantCalls int
		wantSent  bool
	}{
		{name: "transient then success", errs: []error{transient}, wantCalls: 2, wantSent: true},
		{name: "gives up after max attempts", errs: []error{transient, transient, transient, transient}, wantCalls: attempts, wantSent: false},
		{name: "undeliverable is not retried", errs: []error{fmt.Errorf("no slack user: %w", ErrUndeliverable)}, wantCalls: 1, wantSent: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &scriptedNotifier{errs: map[string][]error{owner: tt.errs}}
			d := newTestDispatcher(n, fakeArtifacts{artifact: Artifact{Title: "Doc", OwnerEmail: owner}})
			d.handle(context.Background(), comments.Activity{Kind: comments.ActivityThreadCreated, Slug: "doc", ActorEmail: author, Comment: comments.Comment{ThreadID: "t9", Body: "hi"}})
			if n.calls[owner] != tt.wantCalls {
				t.Errorf("calls = %d, want %d", n.calls[owner], tt.wantCalls)
			}
			if (len(n.sent) == 1) != tt.wantSent {
				t.Errorf("sent = %+v, want sent %v", n.sent, tt.wantSent)
			}
		})
	}
}

func TestDispatcherSkipsWhenArtifactLookupFails(t *testing.T) {
	n := &scriptedNotifier{}
	d := newTestDispatcher(n, fakeArtifacts{err: errors.New("not found")})
	d.handle(context.Background(), comments.Activity{Kind: comments.ActivityThreadCreated, Slug: "gone", ActorEmail: author})
	if len(n.calls) != 0 {
		t.Fatalf("notifier called %v times despite lookup failure", n.calls)
	}
}

func TestCommentActivityNeverBlocks(t *testing.T) {
	d := newTestDispatcher(&scriptedNotifier{}, fakeArtifacts{})
	done := make(chan struct{})
	go func() {
		for i := 0; i < queueSize+50; i++ {
			d.CommentActivity(context.Background(), comments.Activity{Kind: comments.ActivityThreadCreated})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CommentActivity blocked with no consumer")
	}
	if len(d.queue) != queueSize {
		t.Fatalf("queue length = %d, want %d (overflow dropped)", len(d.queue), queueSize)
	}
}

func TestDispatcherRunStopsOnCancel(t *testing.T) {
	n := &scriptedNotifier{}
	d := newTestDispatcher(n, fakeArtifacts{artifact: Artifact{Title: "Doc", OwnerEmail: owner}})
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { d.Run(ctx); close(stopped) }()
	d.CommentActivity(ctx, comments.Activity{Kind: comments.ActivityThreadCreated, Slug: "doc", ActorEmail: author, Comment: comments.Comment{Body: "hi"}})
	deadline := time.After(2 * time.Second)
	for n.sentCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("activity was not delivered")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestExcerpt(t *testing.T) {
	long := strings.Repeat("é", maxExcerptRunes+10)
	got := excerpt(long)
	if r := []rune(got); len(r) != maxExcerptRunes || r[len(r)-1] != '…' {
		t.Fatalf("excerpt rune length = %d, ends %q", len(r), string(r[len(r)-1]))
	}
	if excerpt("short\n\ttext") != "short text" {
		t.Fatalf("whitespace not collapsed: %q", excerpt("short\n\ttext"))
	}
}
