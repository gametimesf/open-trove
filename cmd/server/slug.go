package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
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
