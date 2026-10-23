package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

const (
	slugLength  = 8
	slugCharset = "abcdefghijklmnopqrstuvwxyz0123456789"
	maxSlugLen  = 64
)

var (
	slugRegex     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	reservedSlugs = map[string]bool{
		"healthz": true,
		"upload":  true,
		"mine":    true,
		"swagger": true,
		"mcp":     true,
	}
)

func generateSlug() (string, error) {
	b := make([]byte, slugLength)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(slugCharset))))
		if err != nil {
			return "", fmt.Errorf("generating slug: %w", err)
		}
		b[i] = slugCharset[n.Int64()]
	}
	return string(b), nil
}

// isInternalSlug returns true for any slug that addresses a Trove-internal
// S3 object that must never be served back through the view path. Internal
// keys live under prefixes that start with "_" (e.g. _users/, _sites/,
// _comments/, and the operator-uploaded _prompt). Validated upload slugs cannot start
// with "_" by the slug regex, so this just enforces the same rule on read.
func isInternalSlug(slug string) bool {
	return strings.HasPrefix(slug, "_")
}

func validateSlug(s string) error {
	if len(s) == 0 {
		return fmt.Errorf("slug cannot be empty")
	}
	if len(s) > maxSlugLen {
		return fmt.Errorf("slug exceeds max length of %d", maxSlugLen)
	}
	if !slugRegex.MatchString(s) {
		return fmt.Errorf("slug must contain only lowercase letters, numbers, and hyphens, and must start and end with a letter or number")
	}
	if reservedSlugs[s] {
		return fmt.Errorf("slug %q is reserved", s)
	}
	return nil
}
