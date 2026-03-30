package main

import (
	"testing"
)

func TestGenerateSlug(t *testing.T) {
	slug, err := generateSlug()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(slug) != slugLength {
		t.Errorf("expected length %d, got %d", slugLength, len(slug))
	}
	for _, c := range slug {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			t.Errorf("unexpected character %q in slug %q", c, slug)
		}
	}
}

func TestGenerateSlugUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		slug, err := generateSlug()
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}
		if seen[slug] {
			t.Fatalf("duplicate slug %q on iteration %d", slug, i)
		}
		seen[slug] = true
	}
}

func TestValidateSlug(t *testing.T) {
	tests := []struct {
		name    string
		slug    string
		wantErr bool
	}{
		{"valid short", "a", false},
		{"valid alphanumeric", "abc123", false},
		{"valid with hyphens", "my-cool-file", false},
		{"valid single char", "x", false},
		{"empty", "", true},
		{"starts with hyphen", "-abc", true},
		{"ends with hyphen", "abc-", true},
		{"only hyphen", "-", true},
		{"uppercase", "ABC", true},
		{"spaces", "a b", true},
		{"special chars", "a@b", true},
		{"reserved healthz", "healthz", true},
		{"reserved upload", "upload", true},
		{"too long", string(make([]byte, 65)), true},
		{"max length", "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz01", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSlug(tt.slug)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSlug(%q) error = %v, wantErr %v", tt.slug, err, tt.wantErr)
			}
		})
	}
}
