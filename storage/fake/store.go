// Package fake provides in-memory implementations of storage interfaces for testing.
package fake

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gametimesf/open-trove/comments"
	"github.com/gametimesf/open-trove/storage"
)

type storedObject struct {
	data        []byte
	contentType string
	filename    string
	customSlug  bool
}

// Store is an in-memory fake of storage.Store for testing.
type Store struct {
	mu        sync.RWMutex
	objects   map[string]*storedObject
	manifests map[string]*storage.UserManifest
	comments  map[string][]comments.Comment
}

// NewStore creates a ready-to-use fake Store.
func NewStore() *Store {
	return &Store{
		objects:   make(map[string]*storedObject),
		manifests: make(map[string]*storage.UserManifest),
		comments:  make(map[string][]comments.Comment),
	}
}

func (s *Store) Put(_ context.Context, slug string, body io.Reader, contentType, filename string, customSlug, overwrite bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !overwrite {
		if _, exists := s.objects[slug]; exists {
			return storage.ErrSlugConflict
		}
	}

	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}

	s.objects[slug] = &storedObject{
		data:        data,
		contentType: contentType,
		filename:    filename,
		customSlug:  customSlug,
	}
	return nil
}

func (s *Store) Get(_ context.Context, slug string, _ string) (io.ReadCloser, *storage.FileMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, ok := s.objects[slug]
	if !ok {
		return nil, nil, storage.ErrNotFound
	}

	return io.NopCloser(bytes.NewReader(obj.data)), &storage.FileMetadata{
		ContentType: obj.contentType,
		Filename:    obj.filename,
		Version:     objectVersion(obj.data),
		CustomSlug:  obj.customSlug,
	}, nil
}

func (s *Store) Delete(_ context.Context, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[slug]; !ok {
		return storage.ErrNotFound
	}
	delete(s.objects, slug)
	return nil
}

func (s *Store) Metadata(_ context.Context, slug string) (*storage.FileMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, ok := s.objects[slug]
	if !ok {
		return nil, storage.ErrNotFound
	}

	return &storage.FileMetadata{
		ContentType: obj.contentType,
		Filename:    obj.filename,
		Version:     objectVersion(obj.data),
		CustomSlug:  obj.customSlug,
	}, nil
}

func (s *Store) RecordUpload(_ context.Context, userID string, record storage.ActivityRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	manifest, ok := s.manifests[userID]
	if !ok {
		manifest = &storage.UserManifest{}
		s.manifests[userID] = manifest
	}
	record.At = time.Now().UTC().Format(time.RFC3339)
	manifest.Uploads = append(manifest.Uploads, record)
	return nil
}

func (s *Store) RecordView(_ context.Context, userID string, record storage.ActivityRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	manifest, ok := s.manifests[userID]
	if !ok {
		manifest = &storage.UserManifest{}
		s.manifests[userID] = manifest
	}
	record.At = time.Now().UTC().Format(time.RFC3339)
	for i, v := range manifest.Views {
		if v.Slug == record.Slug {
			manifest.Views[i] = record
			return nil
		}
	}
	manifest.Views = append(manifest.Views, record)
	return nil
}

func (s *Store) GetManifest(_ context.Context, userID string) (*storage.UserManifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if manifest, ok := s.manifests[userID]; ok {
		return manifest, nil
	}
	return &storage.UserManifest{}, nil
}

func (s *Store) PutSiteFile(_ context.Context, siteSlug, path string, body io.Reader, contentType string) error {
	data, _ := io.ReadAll(body)
	key := siteSlug + "/" + path
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = &storedObject{data: data, contentType: contentType, filename: path}
	return nil
}

func (s *Store) GetSiteFile(_ context.Context, siteSlug, path string) (io.ReadCloser, *storage.FileMetadata, error) {
	key := siteSlug + "/" + path
	s.mu.RLock()
	defer s.mu.RUnlock()
	obj, ok := s.objects[key]
	if !ok {
		return nil, nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(obj.data)), &storage.FileMetadata{
		ContentType: obj.contentType,
		Filename:    obj.filename,
		Version:     objectVersion(obj.data),
	}, nil
}

func (s *Store) HeadSiteFile(_ context.Context, siteSlug, path string) (*storage.FileMetadata, error) {
	key := siteSlug + "/" + path
	s.mu.RLock()
	defer s.mu.RUnlock()
	obj, ok := s.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return &storage.FileMetadata{
		ContentType: obj.contentType,
		Filename:    obj.filename,
		Version:     objectVersion(obj.data),
	}, nil
}

func (s *Store) HeadSite(_ context.Context, siteSlug string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := siteSlug + "/"
	for k := range s.objects {
		if strings.HasPrefix(k, prefix) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) PutSiteManifest(_ context.Context, siteSlug string, m *storage.SiteManifest) error {
	data, _ := json.Marshal(m)
	key := siteSlug + "/_site_manifest.json"
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = &storedObject{data: data, contentType: "application/json", filename: "_site_manifest.json"}
	return nil
}

func (s *Store) CreateComment(_ context.Context, comment comments.Comment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.comments[comment.Slug] = append(s.comments[comment.Slug], comment)
	return nil
}

func (s *Store) UpdateComment(_ context.Context, comment comments.Comment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.comments[comment.Slug] {
		if s.comments[comment.Slug][i].ID == comment.ID {
			s.comments[comment.Slug][i] = comment
			return nil
		}
	}
	return comments.ErrCommentNotFound
}

func (s *Store) GetComment(_ context.Context, slug, id string) (comments.Comment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, comment := range s.comments[slug] {
		if comment.ID == id {
			return comment, nil
		}
	}
	return comments.Comment{}, comments.ErrCommentNotFound
}

func (s *Store) ListComments(_ context.Context, slug string) ([]comments.Comment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]comments.Comment(nil), s.comments[slug]...), nil
}

func (s *Store) DeleteComments(_ context.Context, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.comments, slug)
	return nil
}

func objectVersion(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
