package fake

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/gametimesf/open-trove/storage"
)

func TestPutAndGet(t *testing.T) {
	store := NewStore()
	ctx := context.Background()

	err := store.Put(ctx, "test-slug", bytes.NewReader([]byte("hello")), "text/plain", "hello.txt", false, false)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	body, meta, err := store.Get(ctx, "test-slug", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()

	data, _ := io.ReadAll(body)
	if string(data) != "hello" {
		t.Errorf("expected body 'hello', got %q", string(data))
	}
	if meta.ContentType != "text/plain" {
		t.Errorf("expected content type 'text/plain', got %q", meta.ContentType)
	}
	if meta.Filename != "hello.txt" {
		t.Errorf("expected filename 'hello.txt', got %q", meta.Filename)
	}
}

func TestPutConflict(t *testing.T) {
	store := NewStore()
	ctx := context.Background()

	err := store.Put(ctx, "test", bytes.NewReader([]byte("a")), "text/plain", "a.txt", false, false)
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}

	err = store.Put(ctx, "test", bytes.NewReader([]byte("b")), "text/plain", "b.txt", false, false)
	if err != storage.ErrSlugConflict {
		t.Errorf("expected ErrSlugConflict, got %v", err)
	}
}

func TestPutOverwrite(t *testing.T) {
	store := NewStore()
	ctx := context.Background()

	err := store.Put(ctx, "test", bytes.NewReader([]byte("a")), "text/plain", "a.txt", true, false)
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}

	err = store.Put(ctx, "test", bytes.NewReader([]byte("b")), "text/plain", "b.txt", true, true)
	if err != nil {
		t.Fatalf("overwrite Put: %v", err)
	}

	body, meta, err := store.Get(ctx, "test", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()

	data, _ := io.ReadAll(body)
	if string(data) != "b" {
		t.Errorf("expected body 'b' after overwrite, got %q", string(data))
	}
	if meta.Filename != "b.txt" {
		t.Errorf("expected filename 'b.txt', got %q", meta.Filename)
	}
}

func TestGetNotFound(t *testing.T) {
	store := NewStore()
	ctx := context.Background()

	_, _, err := store.Get(ctx, "nope", "")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMetadata(t *testing.T) {
	store := NewStore()
	ctx := context.Background()

	err := store.Put(ctx, "doc", bytes.NewReader([]byte("<html>")), "text/html; charset=utf-8", "index.html", false, false)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	meta, err := store.Metadata(ctx, "doc")
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta.ContentType != "text/html; charset=utf-8" {
		t.Errorf("expected 'text/html; charset=utf-8', got %q", meta.ContentType)
	}
	if meta.Filename != "index.html" {
		t.Errorf("expected 'index.html', got %q", meta.Filename)
	}
}

func TestMetadataNotFound(t *testing.T) {
	store := NewStore()
	ctx := context.Background()

	_, err := store.Metadata(ctx, "nope")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRecordUpload(t *testing.T) {
	store := NewStore()
	ctx := context.Background()

	err := store.RecordUpload(ctx, "user1", storage.ActivityRecord{Slug: "s1", Filename: "f.txt", ContentType: "text/plain"})
	if err != nil {
		t.Fatalf("RecordUpload: %v", err)
	}

	m, err := store.GetManifest(ctx, "user1")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if len(m.Uploads) != 1 {
		t.Fatalf("expected 1 upload, got %d", len(m.Uploads))
	}
	if m.Uploads[0].Slug != "s1" {
		t.Errorf("expected slug 's1', got %q", m.Uploads[0].Slug)
	}
}

func TestRecordViewDedup(t *testing.T) {
	store := NewStore()
	ctx := context.Background()

	store.RecordView(ctx, "user1", storage.ActivityRecord{Slug: "s1", Filename: "a.txt", ContentType: "text/plain"})
	store.RecordView(ctx, "user1", storage.ActivityRecord{Slug: "s2", Filename: "b.txt", ContentType: "text/plain"})
	store.RecordView(ctx, "user1", storage.ActivityRecord{Slug: "s1", Filename: "a.txt", ContentType: "text/plain"})

	m, _ := store.GetManifest(ctx, "user1")
	if len(m.Views) != 2 {
		t.Errorf("expected 2 views (deduped), got %d", len(m.Views))
	}
}

func TestGetManifestEmpty(t *testing.T) {
	store := NewStore()
	ctx := context.Background()

	m, err := store.GetManifest(ctx, "nobody")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if len(m.Uploads) != 0 || len(m.Views) != 0 {
		t.Errorf("expected empty manifest, got %+v", m)
	}
}
