// Package fake provides in-memory implementations of storage interfaces for testing.
package fake

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

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
}

// NewStore creates a ready-to-use fake Store.
func NewStore() *Store {
	return &Store{
		objects:   make(map[string]*storedObject),
		manifests: make(map[string]*storage.UserManifest),
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
