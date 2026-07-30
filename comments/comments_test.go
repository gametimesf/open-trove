package comments

import (
	"context"
	"errors"
	"testing"
	"time"
)

type repositoryStub struct {
	created []Comment
	updated []Comment
	items   []Comment
	err     error
	gets    int
	lists   int
}

func (r *repositoryStub) CreateComment(_ context.Context, comment Comment) error {
	r.created = append(r.created, comment)
	r.items = append(r.items, comment)
	return r.err
}

func (r *repositoryStub) UpdateComment(_ context.Context, comment Comment) error {
	if r.err != nil {
		return r.err
	}
	r.updated = append(r.updated, comment)
	for i := range r.items {
		if r.items[i].ID == comment.ID {
			r.items[i] = comment
			return nil
		}
	}
	return ErrCommentNotFound
}

func (r *repositoryStub) GetComment(_ context.Context, _ string, id string) (Comment, error) {
	r.gets++
	if r.err != nil {
		return Comment{}, r.err
	}
	for _, comment := range r.items {
		if comment.ID == id {
			return comment, nil
		}
	}
	return Comment{}, ErrCommentNotFound
}

func (r *repositoryStub) ListComments(_ context.Context, _ string) ([]Comment, error) {
	r.lists++
	if r.err != nil {
		return nil, r.err
	}
	return append([]Comment(nil), r.items...), nil
}

func (r *repositoryStub) DeleteComments(_ context.Context, _ string) error {
	return r.err
}

func deterministicService(repo *repositoryStub) *Service {
	now := time.Date(2026, 7, 29, 18, 30, 0, 0, time.UTC)
	nextID := 0
	service := NewService(repo)
	service.now = func() time.Time {
		result := now.Add(time.Duration(nextID) * time.Minute)
		return result
	}
	service.newID = func() string {
		nextID++
		return "comment-" + string(rune('0'+nextID))
	}
	return service
}

func TestServiceCreateAndReply(t *testing.T) {
	repo := &repositoryStub{}
	service := deterministicService(repo)

	root, err := service.Create(context.Background(), "my-report", "author@example.com", "v1", CreateInput{
		Body: "  Make this chart larger.  ",
		Anchor: Anchor{
			Type:     AnchorElement,
			Resource: "index.html",
			StableID: "revenue-chart",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if root.ID != "comment-1" || root.ThreadID != root.ID || root.Body != "Make this chart larger." {
		t.Fatalf("Create() = %#v", root)
	}

	reply, err := service.Reply(context.Background(), "my-report", root.ID, "reply@example.com", "v1", ReplyInput{Body: "On it"})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if reply.ThreadID != root.ID || reply.ParentID != root.ID || reply.Anchor.StableID != "revenue-chart" {
		t.Fatalf("Reply() = %#v", reply)
	}
	if repo.gets != 1 || repo.lists != 0 {
		t.Fatalf("Reply() repository calls = gets %d, lists %d; want direct root lookup", repo.gets, repo.lists)
	}
}

func TestServiceEditAndDeleteEnforceAttributedAuthor(t *testing.T) {
	repo := &repositoryStub{}
	service := deterministicService(repo)
	root, err := service.Create(context.Background(), "report", "author@example.com", "v1", CreateInput{Body: "Original"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Edit(context.Background(), "report", root.ID, "other@example.com", EditInput{Body: "Spoofed"}); !errors.Is(err, ErrCommentForbidden) {
		t.Fatalf("Edit() wrong-author error = %v", err)
	}
	edited, err := service.Edit(context.Background(), "report", root.ID, "author@example.com", EditInput{Body: "Corrected"})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if edited.Body != "Corrected" || edited.EditedAt == nil || edited.EditedByEmail != "author@example.com" {
		t.Fatalf("Edit() = %#v", edited)
	}

	deleted, err := service.Delete(context.Background(), "report", root.ID, "author@example.com")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.Body != "" || deleted.DeletedAt == nil {
		t.Fatalf("Delete() = %#v", deleted)
	}
	if _, err := service.Edit(context.Background(), "report", root.ID, "author@example.com", EditInput{Body: "Back"}); !errors.Is(err, ErrCommentDeleted) {
		t.Fatalf("Edit() deleted error = %v", err)
	}
}

func TestServiceResolveAndReopen(t *testing.T) {
	repo := &repositoryStub{}
	service := deterministicService(repo)
	root, err := service.Create(context.Background(), "report", "author@example.com", "v1", CreateInput{Body: "Review"})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := service.Resolve(context.Background(), "report", root.ID, "reviewer@example.com", ResolveInput{Resolved: true})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.ResolvedAt == nil || resolved.ResolvedByEmail != "reviewer@example.com" {
		t.Fatalf("Resolve() = %#v", resolved)
	}
	if _, err := service.Reply(context.Background(), "report", root.ID, "author@example.com", "v1", ReplyInput{Body: "Late reply"}); !errors.Is(err, ErrThreadResolved) {
		t.Fatalf("Reply() resolved error = %v", err)
	}

	reopened, err := service.Resolve(context.Background(), "report", root.ID, "reviewer@example.com", ResolveInput{Resolved: false})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	if reopened.ResolvedAt != nil || reopened.ResolvedByEmail != "" {
		t.Fatalf("reopen = %#v", reopened)
	}
}

func TestServiceListBuildsThreadsAndFiltersResolution(t *testing.T) {
	older := time.Date(2026, 7, 29, 17, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	resolvedAt := newer.Add(time.Hour)
	deletedAt := newer.Add(2 * time.Hour)
	repo := &repositoryStub{items: []Comment{
		{ID: "open", Slug: "site", CreatedAt: older, Anchor: Anchor{Type: AnchorFile}},
		{ID: "open-reply", ThreadID: "open", ParentID: "open", Slug: "site", Body: "reply", CreatedAt: newer, Anchor: Anchor{Type: AnchorFile}},
		{ID: "resolved", ThreadID: "resolved", Slug: "site", CreatedAt: newer, ResolvedAt: &resolvedAt, Anchor: Anchor{Type: AnchorText, Resource: "index.html", Quote: TextQuote{Exact: "hello"}}},
		{ID: "other-page", ThreadID: "other-page", Slug: "site", CreatedAt: newer, Anchor: Anchor{Type: AnchorElement, Resource: "other.html", StableID: "x"}},
		{ID: "deleted-reply", ThreadID: "open", ParentID: "open", Slug: "site", CreatedAt: newer, DeletedAt: &deletedAt, Anchor: Anchor{Type: AnchorFile}},
	}}

	service := NewService(repo)
	open, err := service.List(context.Background(), "site", "index.html", ResolutionOpen)
	if err != nil {
		t.Fatalf("List(open) error = %v", err)
	}
	if len(open) != 1 || open[0].Root.ID != "open" || len(open[0].Replies) != 1 {
		t.Fatalf("List(open) = %#v", open)
	}
	if open[0].Root.ThreadID != "open" {
		t.Fatalf("legacy root thread id = %q", open[0].Root.ThreadID)
	}

	all, err := service.List(context.Background(), "site", "index.html", ResolutionInclude)
	if err != nil {
		t.Fatalf("List(include) error = %v", err)
	}
	if len(all) != 2 || all[0].Root.ID != "resolved" {
		t.Fatalf("List(include) = %#v", all)
	}

	resolved, err := service.List(context.Background(), "site", "index.html", ResolutionOnly)
	if err != nil || len(resolved) != 1 || resolved[0].Root.ID != "resolved" {
		t.Fatalf("List(only) = %#v, %v", resolved, err)
	}
}

func TestDeletedRootRemainsOnlyWhenRepliesExist(t *testing.T) {
	deletedAt := time.Now().UTC()
	repo := &repositoryStub{items: []Comment{
		{ID: "with-reply", ThreadID: "with-reply", Slug: "report", DeletedAt: &deletedAt, Anchor: Anchor{Type: AnchorFile}},
		{ID: "reply", ThreadID: "with-reply", ParentID: "with-reply", Slug: "report", Body: "Still here", Anchor: Anchor{Type: AnchorFile}},
		{ID: "empty", ThreadID: "empty", Slug: "report", DeletedAt: &deletedAt, Anchor: Anchor{Type: AnchorFile}},
	}}
	got, err := NewService(repo).List(context.Background(), "report", "", ResolutionInclude)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Root.ID != "with-reply" {
		t.Fatalf("List() = %#v", got)
	}
}

func TestServiceCreateValidation(t *testing.T) {
	tests := []struct {
		name  string
		slug  string
		email string
		input CreateInput
	}{
		{name: "slug required", email: "a@example.com", input: CreateInput{Body: "body"}},
		{name: "author required", slug: "report", input: CreateInput{Body: "body"}},
		{name: "body required", slug: "report", email: "a@example.com"},
		{name: "valid anchor type", slug: "report", email: "a@example.com", input: CreateInput{Body: "body", Anchor: Anchor{Type: "region"}}},
		{name: "element anchor needs target", slug: "report", email: "a@example.com", input: CreateInput{Body: "body", Anchor: Anchor{Type: AnchorElement}}},
		{name: "text anchor needs quote", slug: "report", email: "a@example.com", input: CreateInput{Body: "body", Anchor: Anchor{Type: AnchorText}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(&repositoryStub{})
			_, err := service.Create(context.Background(), tt.slug, tt.email, "v1", tt.input)
			if !errors.Is(err, ErrInvalidComment) {
				t.Fatalf("Create() error = %v, want ErrInvalidComment", err)
			}
		})
	}
}
