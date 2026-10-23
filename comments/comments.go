// Package comments defines persistent artifact comment threads independently
// of HTTP and object-storage details.
package comments

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxBodyLength        = 5000
	maxResourceLength    = 2048
	maxSelectorLength    = 2000
	maxAnchorTextLength  = 1000
	maxAnchorLabelLength = 500
)

var (
	ErrInvalidComment   = errors.New("invalid comment")
	ErrCommentNotFound  = errors.New("comment not found")
	ErrCommentForbidden = errors.New("comment mutation forbidden")
	ErrThreadResolved   = errors.New("thread is resolved")
	ErrCommentDeleted   = errors.New("comment is deleted")
)

type AnchorType string

const (
	AnchorFile    AnchorType = "file"
	AnchorElement AnchorType = "element"
	AnchorText    AnchorType = "text"
)

type ResolutionFilter string

const (
	ResolutionOpen    ResolutionFilter = "open"
	ResolutionInclude ResolutionFilter = "include"
	ResolutionOnly    ResolutionFilter = "only"
)

// TextQuote locates selected text using the exact quote plus surrounding
// context. Prefix and suffix allow the viewer to disambiguate repeated text.
type TextQuote struct {
	Exact  string `json:"exact,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	Suffix string `json:"suffix,omitempty"`
}

// Rect is the target's viewport-relative rectangle when the comment was made.
// It is diagnostic fallback data, not the primary persistent anchor.
type Rect struct {
	X      int `json:"x,omitempty"`
	Y      int `json:"y,omitempty"`
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// Anchor identifies what a thread applies to. Resource is empty for a
// single-file artifact and contains the page path for a multi-page site.
type Anchor struct {
	Type        AnchorType `json:"type"`
	Resource    string     `json:"resource,omitempty"`
	StableID    string     `json:"stable_id,omitempty"`
	Selector    string     `json:"selector,omitempty"`
	Label       string     `json:"label,omitempty"`
	Tag         string     `json:"tag,omitempty"`
	Role        string     `json:"role,omitempty"`
	VisibleText string     `json:"visible_text,omitempty"`
	Quote       TextQuote  `json:"quote,omitempty"`
	Rect        Rect       `json:"rect,omitempty"`
}

// Comment is an attributed message. Root comments own the thread anchor and
// resolution state. Replies inherit the root anchor and identify their root
// through ThreadID and ParentID.
type Comment struct {
	ID              string     `json:"id"`
	ThreadID        string     `json:"thread_id"`
	ParentID        string     `json:"parent_id,omitempty"`
	Slug            string     `json:"slug"`
	AuthorEmail     string     `json:"author_email"`
	Body            string     `json:"body,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	EditedAt        *time.Time `json:"edited_at,omitempty"`
	EditedByEmail   string     `json:"edited_by_email,omitempty"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	DeletedByEmail  string     `json:"deleted_by_email,omitempty"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	ResolvedByEmail string     `json:"resolved_by_email,omitempty"`
	ArtifactVersion string     `json:"artifact_version,omitempty"`
	Anchor          Anchor     `json:"anchor"`
}

// Thread contains one root comment and its visible replies.
type Thread struct {
	Root    Comment   `json:"root"`
	Replies []Comment `json:"replies"`
}

// CreateInput is the client-controlled portion of a new root comment.
type CreateInput struct {
	Body   string `json:"body"`
	Anchor Anchor `json:"anchor"`
}

type ReplyInput struct {
	Body string `json:"body"`
}

type EditInput struct {
	Body string `json:"body"`
}

type ResolveInput struct {
	Resolved bool `json:"resolved"`
}

// Repository is the persistence capability consumed by Service.
type Repository interface {
	CreateComment(ctx context.Context, comment Comment) error
	UpdateComment(ctx context.Context, comment Comment) error
	GetComment(ctx context.Context, slug, id string) (Comment, error)
	ListComments(ctx context.Context, slug string) ([]Comment, error)
	DeleteComments(ctx context.Context, slug string) error
}

type Service struct {
	repository Repository
	now        func() time.Time
	newID      func() string
}

func NewService(repository Repository) *Service {
	return &Service{
		repository: repository,
		now:        time.Now,
		newID:      uuid.NewString,
	}
}

func (s *Service) Create(ctx context.Context, slug, authorEmail, artifactVersion string, input CreateInput) (Comment, error) {
	input.Body = strings.TrimSpace(input.Body)
	input.Anchor = normalizeAnchor(input.Anchor)
	if err := validateCreate(slug, authorEmail, input); err != nil {
		return Comment{}, err
	}

	id := s.newID()
	comment := Comment{
		ID:              id,
		ThreadID:        id,
		Slug:            slug,
		AuthorEmail:     authorEmail,
		Body:            input.Body,
		CreatedAt:       s.now().UTC(),
		ArtifactVersion: artifactVersion,
		Anchor:          input.Anchor,
	}
	if err := s.repository.CreateComment(ctx, comment); err != nil {
		return Comment{}, fmt.Errorf("creating comment: %w", err)
	}
	return comment, nil
}

func (s *Service) Reply(ctx context.Context, slug, threadID, authorEmail, artifactVersion string, input ReplyInput) (Comment, error) {
	body, err := validateBody(input.Body)
	if err != nil {
		return Comment{}, err
	}
	if strings.TrimSpace(authorEmail) == "" {
		return Comment{}, invalid("author email is required")
	}

	root, err := s.storedComment(ctx, slug, threadID)
	if err != nil {
		return Comment{}, err
	}
	if root.ParentID != "" {
		return Comment{}, ErrCommentNotFound
	}
	if root.DeletedAt != nil {
		return Comment{}, ErrCommentDeleted
	}
	if root.ResolvedAt != nil {
		return Comment{}, ErrThreadResolved
	}

	reply := Comment{
		ID:              s.newID(),
		ThreadID:        root.ID,
		ParentID:        root.ID,
		Slug:            slug,
		AuthorEmail:     authorEmail,
		Body:            body,
		CreatedAt:       s.now().UTC(),
		ArtifactVersion: artifactVersion,
		Anchor:          root.Anchor,
	}
	if err := s.repository.CreateComment(ctx, reply); err != nil {
		return Comment{}, fmt.Errorf("creating reply: %w", err)
	}
	return reply, nil
}

func (s *Service) Edit(ctx context.Context, slug, commentID, actorEmail string, input EditInput) (Comment, error) {
	body, err := validateBody(input.Body)
	if err != nil {
		return Comment{}, err
	}
	comment, err := s.commentForMutation(ctx, slug, commentID, actorEmail)
	if err != nil {
		return Comment{}, err
	}
	now := s.now().UTC()
	comment.Body = body
	comment.EditedAt = &now
	comment.EditedByEmail = actorEmail
	if err := s.repository.UpdateComment(ctx, comment); err != nil {
		return Comment{}, fmt.Errorf("updating comment: %w", err)
	}
	return comment, nil
}

// Delete clears the comment body and leaves a tombstone so a deleted root can
// continue to own existing replies. Deleted replies are omitted from lists.
func (s *Service) Delete(ctx context.Context, slug, commentID, actorEmail string) (Comment, error) {
	comment, err := s.commentForMutation(ctx, slug, commentID, actorEmail)
	if err != nil {
		return Comment{}, err
	}
	now := s.now().UTC()
	comment.Body = ""
	comment.DeletedAt = &now
	comment.DeletedByEmail = actorEmail
	if err := s.repository.UpdateComment(ctx, comment); err != nil {
		return Comment{}, fmt.Errorf("deleting comment: %w", err)
	}
	return comment, nil
}

func (s *Service) Resolve(ctx context.Context, slug, threadID, actorEmail string, input ResolveInput) (Comment, error) {
	if strings.TrimSpace(actorEmail) == "" {
		return Comment{}, invalid("actor email is required")
	}
	root, err := s.storedComment(ctx, slug, threadID)
	if err != nil {
		return Comment{}, err
	}
	if root.ParentID != "" {
		return Comment{}, ErrCommentNotFound
	}
	if root.DeletedAt != nil {
		return Comment{}, ErrCommentDeleted
	}

	if input.Resolved {
		if root.ResolvedAt != nil {
			return root, nil
		}
		now := s.now().UTC()
		root.ResolvedAt = &now
		root.ResolvedByEmail = actorEmail
	} else {
		if root.ResolvedAt == nil {
			return root, nil
		}
		root.ResolvedAt = nil
		root.ResolvedByEmail = ""
	}
	if err := s.repository.UpdateComment(ctx, root); err != nil {
		return Comment{}, fmt.Errorf("updating thread resolution: %w", err)
	}
	return root, nil
}

// List returns threads for the requested resource. Whole-artifact threads are
// included on every site page. Open threads are the default view.
func (s *Service) List(ctx context.Context, slug, resource string, resolution ResolutionFilter) ([]Thread, error) {
	items, err := s.listStored(ctx, slug)
	if err != nil {
		return nil, err
	}
	if resolution == "" {
		resolution = ResolutionOpen
	}
	if resolution != ResolutionOpen && resolution != ResolutionInclude && resolution != ResolutionOnly {
		return nil, invalid("unsupported resolution filter %q", resolution)
	}

	resource = cleanResource(resource)
	roots := make(map[string]Comment)
	replies := make(map[string][]Comment)
	for _, item := range items {
		if item.ParentID == "" {
			roots[item.ID] = item
			continue
		}
		if item.DeletedAt == nil {
			replies[item.ThreadID] = append(replies[item.ThreadID], item)
		}
	}

	threads := make([]Thread, 0, len(roots))
	for id, root := range roots {
		rootResource := cleanResource(root.Anchor.Resource)
		if rootResource != "" && rootResource != resource {
			continue
		}
		threadReplies := replies[id]
		if root.DeletedAt != nil && len(threadReplies) == 0 {
			continue
		}
		resolved := root.ResolvedAt != nil
		if resolution == ResolutionOpen && resolved {
			continue
		}
		if resolution == ResolutionOnly && !resolved {
			continue
		}
		sort.SliceStable(threadReplies, func(i, j int) bool {
			return threadReplies[i].CreatedAt.Before(threadReplies[j].CreatedAt)
		})
		if threadReplies == nil {
			threadReplies = []Comment{}
		}
		threads = append(threads, Thread{Root: root, Replies: threadReplies})
	}
	sort.SliceStable(threads, func(i, j int) bool {
		return threads[i].Root.CreatedAt.After(threads[j].Root.CreatedAt)
	})
	return threads, nil
}

func (s *Service) commentForMutation(ctx context.Context, slug, commentID, actorEmail string) (Comment, error) {
	if strings.TrimSpace(actorEmail) == "" {
		return Comment{}, invalid("actor email is required")
	}
	comment, err := s.storedComment(ctx, slug, commentID)
	if err != nil {
		return Comment{}, err
	}
	if comment.DeletedAt != nil {
		return Comment{}, ErrCommentDeleted
	}
	if !strings.EqualFold(comment.AuthorEmail, actorEmail) {
		return Comment{}, ErrCommentForbidden
	}
	return comment, nil
}

func (s *Service) storedComment(ctx context.Context, slug, commentID string) (Comment, error) {
	if strings.TrimSpace(slug) == "" || strings.TrimSpace(commentID) == "" {
		return Comment{}, ErrCommentNotFound
	}
	comment, err := s.repository.GetComment(ctx, slug, commentID)
	if err != nil {
		if errors.Is(err, ErrCommentNotFound) {
			return Comment{}, ErrCommentNotFound
		}
		return Comment{}, fmt.Errorf("getting comment: %w", err)
	}
	if comment.ThreadID == "" {
		if comment.ParentID == "" {
			comment.ThreadID = comment.ID
		} else {
			comment.ThreadID = comment.ParentID
		}
	}
	return comment, nil
}

func (s *Service) listStored(ctx context.Context, slug string) ([]Comment, error) {
	if strings.TrimSpace(slug) == "" {
		return nil, invalid("slug is required")
	}
	items, err := s.repository.ListComments(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("listing comments: %w", err)
	}
	for i := range items {
		if items[i].ThreadID == "" {
			if items[i].ParentID == "" {
				items[i].ThreadID = items[i].ID
			} else {
				items[i].ThreadID = items[i].ParentID
			}
		}
	}
	return items, nil
}

func normalizeAnchor(anchor Anchor) Anchor {
	if anchor.Type == "" {
		anchor.Type = AnchorFile
	}
	anchor.Resource = cleanResource(anchor.Resource)
	anchor.StableID = strings.TrimSpace(anchor.StableID)
	anchor.Selector = strings.TrimSpace(anchor.Selector)
	anchor.Label = strings.TrimSpace(anchor.Label)
	anchor.Tag = strings.TrimSpace(anchor.Tag)
	anchor.Role = strings.TrimSpace(anchor.Role)
	anchor.VisibleText = strings.TrimSpace(anchor.VisibleText)
	anchor.Quote.Exact = strings.TrimSpace(anchor.Quote.Exact)
	anchor.Quote.Prefix = strings.TrimSpace(anchor.Quote.Prefix)
	anchor.Quote.Suffix = strings.TrimSpace(anchor.Quote.Suffix)
	return anchor
}

func cleanResource(resource string) string {
	return strings.TrimPrefix(strings.TrimSpace(resource), "/")
}

func validateCreate(slug, authorEmail string, input CreateInput) error {
	if strings.TrimSpace(slug) == "" {
		return invalid("slug is required")
	}
	if strings.TrimSpace(authorEmail) == "" {
		return invalid("author email is required")
	}
	if _, err := validateBody(input.Body); err != nil {
		return err
	}
	if len(input.Anchor.Resource) > maxResourceLength {
		return invalid("anchor resource is too long")
	}
	if len(input.Anchor.Selector) > maxSelectorLength {
		return invalid("anchor selector is too long")
	}
	for name, value := range map[string]string{
		"stable_id": input.Anchor.StableID,
		"label":     input.Anchor.Label,
		"tag":       input.Anchor.Tag,
		"role":      input.Anchor.Role,
	} {
		if len(value) > maxAnchorLabelLength {
			return invalid("anchor %s is too long", name)
		}
	}
	for name, value := range map[string]string{
		"visible_text": input.Anchor.VisibleText,
		"quote.exact":  input.Anchor.Quote.Exact,
		"quote.prefix": input.Anchor.Quote.Prefix,
		"quote.suffix": input.Anchor.Quote.Suffix,
	} {
		if len(value) > maxAnchorTextLength {
			return invalid("anchor %s is too long", name)
		}
	}

	switch input.Anchor.Type {
	case AnchorFile:
	case AnchorElement:
		if input.Anchor.StableID == "" && input.Anchor.Selector == "" {
			return invalid("element anchor requires stable_id or selector")
		}
	case AnchorText:
		if input.Anchor.Quote.Exact == "" {
			return invalid("text anchor requires quote.exact")
		}
	default:
		return invalid("unsupported anchor type %q", input.Anchor.Type)
	}
	return nil
}

func validateBody(value string) (string, error) {
	body := strings.TrimSpace(value)
	if body == "" {
		return "", invalid("body is required")
	}
	if len(body) > maxBodyLength {
		return "", invalid("body exceeds %d characters", maxBodyLength)
	}
	return body, nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidComment, fmt.Sprintf(format, args...))
}
