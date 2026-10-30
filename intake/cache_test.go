package intake

import (
	"context"
	"errors"
	"testing"
)

// memoryCache is an in-memory VerdictCache with injectable failures.
type memoryCache struct {
	keys    map[string]bool
	readErr error
	puts    int
}

func (m *memoryCache) HasAllowedVerdict(_ context.Context, key string) (bool, error) {
	if m.readErr != nil {
		return false, m.readErr
	}
	return m.keys[key], nil
}

func (m *memoryCache) PutAllowedVerdict(_ context.Context, key string) error {
	m.puts++
	m.keys[key] = true
	return nil
}

// countingInspector returns a fixed result, counts calls and reports a
// settable fingerprint.
type countingInspector struct {
	verdict        *Verdict
	err            error
	fingerprint    string
	fingerprintErr error
	calls          int
}

func (c *countingInspector) Inspect(context.Context, Input) (*Verdict, error) {
	c.calls++
	return c.verdict, c.err
}

func (c *countingInspector) Fingerprint(context.Context) (string, error) {
	return c.fingerprint, c.fingerprintErr
}

func TestWithCache(t *testing.T) {
	page := Input{Filename: "index.html", ContentType: "text/html", Body: []byte("<p>hi</p>")}
	allowed := &Verdict{Allowed: true}
	blocked := &Verdict{Allowed: false, Reason: "no"}

	tests := []struct {
		name      string
		inner     *countingInspector
		cache     *memoryCache
		first     Input
		change    func(*countingInspector)
		second    Input
		wantCalls int
		wantPuts  int
	}{
		{
			name:      "same input is inspected once",
			inner:     &countingInspector{verdict: allowed, fingerprint: "p1"},
			first:     page,
			second:    page,
			wantCalls: 1,
			wantPuts:  1,
		},
		{
			name:      "changed body is inspected",
			inner:     &countingInspector{verdict: allowed, fingerprint: "p1"},
			first:     page,
			second:    Input{Filename: page.Filename, ContentType: page.ContentType, Body: []byte("<p>bye</p>")},
			wantCalls: 2,
			wantPuts:  2,
		},
		{
			name:      "changed filename is inspected",
			inner:     &countingInspector{verdict: allowed, fingerprint: "p1"},
			first:     page,
			second:    Input{Filename: "salaries.html", ContentType: page.ContentType, Body: page.Body},
			wantCalls: 2,
			wantPuts:  2,
		},
		{
			name:      "changed policy is inspected",
			inner:     &countingInspector{verdict: allowed, fingerprint: "p1"},
			first:     page,
			change:    func(c *countingInspector) { c.fingerprint = "p2" },
			second:    page,
			wantCalls: 2,
			wantPuts:  2,
		},
		{
			name:      "blocked verdicts are not cached",
			inner:     &countingInspector{verdict: blocked, fingerprint: "p1"},
			first:     page,
			second:    page,
			wantCalls: 2,
			wantPuts:  0,
		},
		{
			name:      "inspector errors are not cached",
			inner:     &countingInspector{err: ErrInspector, fingerprint: "p1"},
			first:     page,
			second:    page,
			wantCalls: 2,
			wantPuts:  0,
		},
		{
			name:      "cache read failure falls back to inspecting",
			inner:     &countingInspector{verdict: allowed, fingerprint: "p1"},
			cache:     &memoryCache{keys: map[string]bool{}, readErr: errors.New("s3 down")},
			first:     page,
			second:    page,
			wantCalls: 2,
			wantPuts:  2,
		},
		{
			name:      "fingerprint failure skips the cache",
			inner:     &countingInspector{verdict: allowed, fingerprintErr: errors.New("no guidance")},
			first:     page,
			second:    page,
			wantCalls: 2,
			wantPuts:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := tt.cache
			if cache == nil {
				cache = &memoryCache{keys: map[string]bool{}}
			}
			insp := WithCache(tt.inner, cache)
			ctx := context.Background()

			if _, err := insp.Inspect(ctx, tt.first); err != nil && !errors.Is(err, tt.inner.err) {
				t.Fatalf("first Inspect: %v", err)
			}
			if tt.change != nil {
				tt.change(tt.inner)
			}
			got, err := insp.Inspect(ctx, tt.second)
			if !errors.Is(err, tt.inner.err) {
				t.Fatalf("second Inspect error = %v, want %v", err, tt.inner.err)
			}
			if err == nil && got.Allowed != tt.inner.verdict.Allowed {
				t.Errorf("second verdict = %+v, want %+v", got, tt.inner.verdict)
			}
			if tt.inner.calls != tt.wantCalls {
				t.Errorf("inspector calls = %d, want %d", tt.inner.calls, tt.wantCalls)
			}
			if cache.puts != tt.wantPuts {
				t.Errorf("cache puts = %d, want %d", cache.puts, tt.wantPuts)
			}
		})
	}
}

func TestWithCacheLeavesInspectorsWithoutFingerprint(t *testing.T) {
	if got := WithCache(NoOp{}, &memoryCache{keys: map[string]bool{}}); got != (NoOp{}) {
		t.Errorf("WithCache(NoOp) = %T, want NoOp unchanged", got)
	}
}

func TestCacheKeyFieldsDoNotRunTogether(t *testing.T) {
	a := CacheKey("p", Input{Filename: "ab", ContentType: "c"})
	b := CacheKey("p", Input{Filename: "a", ContentType: "bc"})
	if a == b {
		t.Error("different filename/content type split produced the same key")
	}
	if len(a) != 64 {
		t.Errorf("key length = %d, want 64 hex chars", len(a))
	}
}
