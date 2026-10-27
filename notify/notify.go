// Package notify turns comment activity into per-person notifications and
// delivers them through a Notifier (for example Slack direct messages).
// Delivery is asynchronous so comment writes never wait on, or fail because
// of, a notification channel. See docs/slack.md.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gametimesf/open-trove/comments"
)

// ErrUndeliverable marks a notification that can never be delivered to its
// recipient (for example, no account on the channel); it is not retried.
var ErrUndeliverable = errors.New("notify: recipient cannot be notified")

// Notification is one message for one recipient.
type Notification struct {
	Kind           comments.ActivityKind
	RecipientEmail string
	ActorEmail     string
	// RecipientIsOwner reports whether the recipient owns the artifact.
	RecipientIsOwner bool
	ArtifactTitle    string
	ArtifactURL      string
	// Excerpt is a shortened comment body; empty for resolution changes.
	Excerpt string
}

// Notifier delivers a notification. Return an error wrapping
// ErrUndeliverable for failures that retrying cannot fix.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// Artifact is what notifications need to know about the commented file.
type Artifact struct {
	Title      string
	OwnerEmail string
}

// ArtifactLookup finds an artifact's title and owner.
type ArtifactLookup interface {
	Artifact(ctx context.Context, slug string) (Artifact, error)
}

// CommentLister lists an artifact's stored comments.
type CommentLister interface {
	ListComments(ctx context.Context, slug string) ([]comments.Comment, error)
}

const (
	maxExcerptRunes = 280
	queueSize       = 1000
	attempts        = 3
	attemptTimeout  = 10 * time.Second
)

// Recipients decides who hears about an activity: the artifact owner for new
// threads and replies, everyone already in the thread for replies, and the
// thread's author when it is resolved or reopened. The actor never notifies
// themselves.
func Recipients(activity comments.Activity, ownerEmail string, thread []comments.Comment) []string {
	var candidates []string
	switch activity.Kind {
	case comments.ActivityThreadCreated:
		candidates = append(candidates, ownerEmail)
	case comments.ActivityReplyCreated:
		candidates = append(candidates, ownerEmail)
		for _, c := range thread {
			if c.ThreadID == activity.Comment.ThreadID && c.DeletedAt == nil {
				candidates = append(candidates, c.AuthorEmail)
			}
		}
	case comments.ActivityThreadResolved, comments.ActivityThreadReopened:
		candidates = append(candidates, activity.Comment.AuthorEmail)
	}

	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, email := range candidates {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" || strings.EqualFold(email, activity.ActorEmail) {
			continue
		}
		if _, dup := seen[email]; dup {
			continue
		}
		seen[email] = struct{}{}
		out = append(out, email)
	}
	return out
}

// Dispatcher queues comment activity and delivers notifications in the
// background. It implements comments.Observer.
type Dispatcher struct {
	notifier  Notifier
	artifacts ArtifactLookup
	comments  CommentLister
	baseURL   string
	queue     chan comments.Activity
	sleep     func(context.Context, time.Duration) error
}

// NewDispatcher returns a dispatcher; call Run to start delivery.
func NewDispatcher(notifier Notifier, artifacts ArtifactLookup, lister CommentLister, baseURL string) *Dispatcher {
	return &Dispatcher{
		notifier:  notifier,
		artifacts: artifacts,
		comments:  lister,
		baseURL:   strings.TrimRight(baseURL, "/"),
		queue:     make(chan comments.Activity, queueSize),
		sleep:     sleepContext,
	}
}

// CommentActivity enqueues activity without blocking. When the queue is full
// the activity is dropped and logged rather than slowing the comment write.
func (d *Dispatcher) CommentActivity(_ context.Context, activity comments.Activity) {
	select {
	case d.queue <- activity:
	default:
		log.Printf("WARN  notification dropped queue_full kind=%s slug=%s comment_id=%s", activity.Kind, activity.Slug, activity.Comment.ID)
	}
}

// Run delivers queued activity until ctx is done.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case activity := <-d.queue:
			d.handle(ctx, activity)
		}
	}
}

func (d *Dispatcher) handle(ctx context.Context, activity comments.Activity) {
	artifact, err := d.artifacts.Artifact(ctx, activity.Slug)
	if err != nil {
		log.Printf("WARN  notification skipped kind=%s slug=%s reason=artifact_lookup: %v", activity.Kind, activity.Slug, err)
		return
	}
	var thread []comments.Comment
	if activity.Kind == comments.ActivityReplyCreated {
		if thread, err = d.comments.ListComments(ctx, activity.Slug); err != nil {
			log.Printf("WARN  notification skipped kind=%s slug=%s reason=thread_lookup: %v", activity.Kind, activity.Slug, err)
			return
		}
	}

	for _, recipient := range Recipients(activity, artifact.OwnerEmail, thread) {
		n := Notification{
			Kind:             activity.Kind,
			RecipientEmail:   recipient,
			ActorEmail:       activity.ActorEmail,
			RecipientIsOwner: strings.EqualFold(recipient, artifact.OwnerEmail),
			ArtifactTitle:    artifact.Title,
			ArtifactURL:      d.artifactURL(activity),
		}
		if activity.Kind == comments.ActivityThreadCreated || activity.Kind == comments.ActivityReplyCreated {
			n.Excerpt = excerpt(activity.Comment.Body)
		}
		if err := d.deliver(ctx, n); err != nil {
			log.Printf("WARN  notification failed kind=%s slug=%s recipient=%q: %v", n.Kind, activity.Slug, recipient, err)
			continue
		}
		log.Printf("INFO  notification sent kind=%s slug=%s recipient=%q", n.Kind, activity.Slug, recipient)
	}
}

// deliver retries transient failures with backoff; undeliverable recipients
// and a stopped dispatcher end immediately.
func (d *Dispatcher) deliver(ctx context.Context, n Notification) error {
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		err = d.notifier.Notify(attemptCtx, n)
		cancel()
		if err == nil || errors.Is(err, ErrUndeliverable) || attempt == attempts {
			return err
		}
		if sleepErr := d.sleep(ctx, time.Duration(attempt)*time.Second); sleepErr != nil {
			return fmt.Errorf("%w (stopped before retry: %w)", err, sleepErr)
		}
	}
	return err
}

func (d *Dispatcher) artifactURL(activity comments.Activity) string {
	return CommentURL(d.baseURL, activity.Slug, activity.Comment.Anchor.Resource, activity.Comment.ThreadID)
}

// CommentURL links to a comment thread in the Trove viewer, which opens the
// comments sidebar on that thread. resource selects a site page; threadID may
// be empty to link to the artifact only.
func CommentURL(baseURL, slug, resource, threadID string) string {
	u := baseURL + "/" + url.PathEscape(slug)
	var query []string
	if resource != "" {
		query = append(query, "page="+url.QueryEscape(resource))
	}
	if threadID != "" {
		query = append(query, "comment="+url.QueryEscape(threadID))
	}
	if len(query) > 0 {
		u += "?" + strings.Join(query, "&")
	}
	return u
}

func excerpt(body string) string {
	body = strings.Join(strings.Fields(body), " ")
	if utf8.RuneCountInString(body) <= maxExcerptRunes {
		return body
	}
	runes := []rune(body)
	return strings.TrimSpace(string(runes[:maxExcerptRunes-1])) + "…"
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
