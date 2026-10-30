package intake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
)

// VerdictCache remembers inputs an inspector has allowed. Keys are hex
// SHA-256 digests built by CacheKey.
type VerdictCache interface {
	HasAllowedVerdict(ctx context.Context, key string) (bool, error)
	PutAllowedVerdict(ctx context.Context, key string) error
}

// Fingerprinter is implemented by inspectors whose verdicts depend on
// settings that can change at runtime, such as the model or guidance. The
// fingerprint changes whenever any of them do.
type Fingerprinter interface {
	Fingerprint(ctx context.Context) (string, error)
}

// WithCache wraps an inspector so an input it already allowed under the
// same fingerprint isn't inspected again; re-uploading a site then only
// inspects the files that changed. Only allowed verdicts are cached, so
// blocked content is always inspected afresh. Inspectors without a
// fingerprint are returned unchanged.
func WithCache(inner Inspector, cache VerdictCache) Inspector {
	fp, ok := inner.(Fingerprinter)
	if !ok || cache == nil {
		return inner
	}
	return &cachedInspector{inner: inner, fingerprinter: fp, cache: cache}
}

type cachedInspector struct {
	inner         Inspector
	fingerprinter Fingerprinter
	cache         VerdictCache
}

// Inspect returns a cached allow when there is one and otherwise inspects
// the input, remembering it if allowed. Cache failures fall back to
// inspecting, so they cost time but never let unchecked content through.
func (c *cachedInspector) Inspect(ctx context.Context, in Input) (*Verdict, error) {
	fingerprint, err := c.fingerprinter.Fingerprint(ctx)
	if err != nil {
		log.Printf("WARN  intake cache skipped filename=%q: %v", in.Filename, err)
		return c.inner.Inspect(ctx, in)
	}
	key := CacheKey(fingerprint, in)
	switch allowed, err := c.cache.HasAllowedVerdict(ctx, key); {
	case err != nil:
		log.Printf("WARN  intake cache lookup failed filename=%q: %v", in.Filename, err)
	case allowed:
		log.Printf("INFO  intake cache hit filename=%q", in.Filename)
		return &Verdict{Allowed: true}, nil
	}

	verdict, err := c.inner.Inspect(ctx, in)
	if err == nil && verdict.Allowed {
		if err := c.cache.PutAllowedVerdict(ctx, key); err != nil {
			log.Printf("WARN  intake cache store failed filename=%q: %v", in.Filename, err)
		}
	}
	return verdict, err
}

// CacheKey identifies one inspection: the inspector's fingerprint plus
// everything it sees about the input. Fields are length-prefixed so
// different inputs can't produce the same key.
func CacheKey(fingerprint string, in Input) string {
	body := sha256.Sum256(in.Body)
	h := sha256.New()
	fmt.Fprintf(h, "%d:%s%d:%s%d:%s%x", len(fingerprint), fingerprint, len(in.Filename), in.Filename, len(in.ContentType), in.ContentType, body)
	return hex.EncodeToString(h.Sum(nil))
}
