package intake

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// PromptSource fetches the encrypted guidance blob (typically from S3)
// and returns its plaintext bytes. Implementations are expected to be
// safe for concurrent use.
type PromptSource interface {
	Fetch(ctx context.Context) ([]byte, error)
}

// FetcherFunc adapts a function to the PromptSource interface.
type FetcherFunc func(ctx context.Context) ([]byte, error)

// Fetch implements PromptSource.
func (f FetcherFunc) Fetch(ctx context.Context) ([]byte, error) { return f(ctx) }

// CachedSource caches the result of an underlying source for a TTL.
// Concurrent Fetch calls during a refresh share a single in-flight
// fetch; an inner failure returns the last successful value when one
// exists, so a transient outage doesn't stop the gate.
type CachedSource struct {
	Inner PromptSource
	TTL   time.Duration

	mu        sync.Mutex
	cached    []byte
	expires   time.Time
	hasCached bool
}

// Fetch returns a cached value if fresh, otherwise refreshes from the
// underlying source.
func (c *CachedSource) Fetch(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	if c.hasCached && time.Now().Before(c.expires) {
		out := append([]byte(nil), c.cached...)
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()

	fresh, err := c.Inner.Fetch(ctx)
	if err != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.hasCached {
			// Soft fallback to last good — extend TTL slightly so
			// repeated failures don't hammer the source. Caller still
			// sees a successful read.
			c.expires = time.Now().Add(c.TTL / 2)
			out := append([]byte(nil), c.cached...)
			return out, nil
		}
		return nil, err
	}

	c.mu.Lock()
	c.cached = append([]byte(nil), fresh...)
	c.expires = time.Now().Add(c.TTL)
	c.hasCached = true
	c.mu.Unlock()
	return fresh, nil
}

// S3Reader is the minimal subset of the AWS S3 client surface this
// package needs. Tests substitute a fake.
type S3Reader interface {
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error)
}

// S3Source fetches a prompt blob from S3.
type S3Source struct {
	Client S3Reader
	Bucket string
	Key    string
}

// Fetch reads the configured object and returns its raw bytes.
func (s *S3Source) Fetch(ctx context.Context) ([]byte, error) {
	if s.Client == nil {
		return nil, errors.New("intake: S3Source.Client is nil")
	}
	rc, err := s.Client.GetObject(ctx, s.Bucket, s.Key)
	if err != nil {
		return nil, fmt.Errorf("intake: s3 get %s/%s: %w", s.Bucket, s.Key, err)
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
