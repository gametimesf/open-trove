package intake

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type fakeS3 struct {
	bucket, key string
	body        []byte
	err         error
	calls       int32
}

func (f *fakeS3) GetObject(_ context.Context, bucket, key string) (io.ReadCloser, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return nil, f.err
	}
	if bucket != f.bucket || key != f.key {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

func TestS3SourceFetch(t *testing.T) {
	want := []byte("encrypted blob")
	s := &S3Source{
		Client: &fakeS3{bucket: "b", key: "_prompt", body: want},
		Bucket: "b",
		Key:    "_prompt",
	}
	got, err := s.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCachedSourceServesCacheUntilExpiry(t *testing.T) {
	var calls int32
	src := FetcherFunc(func(_ context.Context) ([]byte, error) {
		atomic.AddInt32(&calls, 1)
		return []byte("v1"), nil
	})
	c := &CachedSource{Inner: src, TTL: 50 * time.Millisecond}

	for i := 0; i < 5; i++ {
		got, err := c.Fetch(context.Background())
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if string(got) != "v1" {
			t.Errorf("got %q", got)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected 1 call within TTL, got %d", got)
	}

	time.Sleep(60 * time.Millisecond)
	if _, err := c.Fetch(context.Background()); err != nil {
		t.Fatalf("post-expiry: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("expected 2 calls after expiry, got %d", got)
	}
}

func TestCachedSourceFallsBackToLastGoodOnError(t *testing.T) {
	var failing atomic.Bool
	src := FetcherFunc(func(_ context.Context) ([]byte, error) {
		if failing.Load() {
			return nil, errors.New("upstream failed")
		}
		return []byte("good"), nil
	})
	c := &CachedSource{Inner: src, TTL: 10 * time.Millisecond}

	if got, err := c.Fetch(context.Background()); err != nil || string(got) != "good" {
		t.Fatalf("first Fetch: %v %q", err, got)
	}

	time.Sleep(15 * time.Millisecond) // expire
	failing.Store(true)
	got, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("expected fallback to last good, got error: %v", err)
	}
	if string(got) != "good" {
		t.Errorf("got %q, want last-good 'good'", got)
	}
}

func TestCachedSourceFirstErrorPropagates(t *testing.T) {
	src := FetcherFunc(func(_ context.Context) ([]byte, error) {
		return nil, errors.New("never seen good")
	})
	c := &CachedSource{Inner: src, TTL: time.Second}
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Error("expected error when no prior success")
	}
}
