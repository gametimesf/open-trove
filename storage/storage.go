// Package storage defines the store interface, shared types, and backend
// implementations for trove. Use NewStore to create a store by type name.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/gametimesf/open-trove/internal/config"
)

var (
	ErrSlugConflict = errors.New("slug already exists")
	ErrNotFound     = errors.New("object not found")
)

// Store is the interface that storage backends must implement.
type Store interface {
	Put(ctx context.Context, slug string, body io.Reader, contentType, filename string, customSlug, overwrite bool) error
	Get(ctx context.Context, slug string, rangeHeader string) (io.ReadCloser, *FileMetadata, error)
	Delete(ctx context.Context, slug string) error
	Metadata(ctx context.Context, slug string) (*FileMetadata, error)
	RecordUpload(ctx context.Context, userID string, record ActivityRecord) error
	RecordView(ctx context.Context, userID string, record ActivityRecord) error
	GetManifest(ctx context.Context, userID string) (*UserManifest, error)
}

// FileMetadata holds metadata about a stored file.
type FileMetadata struct {
	ContentType   string
	Filename      string
	CustomSlug    bool
	ContentRange  string // set on 206 partial-content responses
	ContentLength int64  // bytes in this response body; set on 206 partial-content responses
}

// ActivityRecord represents a single upload or view event.
type ActivityRecord struct {
	Slug        string `json:"slug"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	At          string `json:"at"`
}

// UserManifest tracks a user's upload and view history.
type UserManifest struct {
	Uploads []ActivityRecord `json:"uploads"`
	Views   []ActivityRecord `json:"views"`
}

// NewStore creates a Store based on the store config's Type field.
func NewStore(ctx context.Context, cfg config.Store) (Store, error) {
	switch cfg.Type {
	case "s3":
		return newS3Store(ctx, S3Config{
			Bucket:   cfg.S3.Bucket,
			Endpoint: cfg.S3.Endpoint,
			Region:   cfg.S3.Region,
		})
	default:
		return nil, fmt.Errorf("storage: unknown store type %q", cfg.Type)
	}
}
