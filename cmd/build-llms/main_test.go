package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildGuide(t *testing.T) {
	for _, tt := range []struct {
		name    string
		extra   []byte
		mode    string
		want    []byte
		invalid bool
	}{
		{name: "default", want: []byte("default\r\n")},
		{name: "append", extra: []byte("  Café →\r\n\n"), mode: "append", want: []byte("default\r\n\n\n  Café →\r\n\n")},
		{name: "override", extra: []byte("replacement\n"), mode: "override", want: []byte("replacement\n")},
		{name: "empty", extra: []byte{}, mode: "append", invalid: true},
		{name: "blank", extra: []byte(" \n\t"), mode: "override", invalid: true},
		{name: "encoding", extra: []byte{0xff}, mode: "append", invalid: true},
		{name: "limit", extra: bytes.Repeat([]byte("x"), 65536), mode: "override", want: bytes.Repeat([]byte("x"), 65536)},
		{name: "oversize", extra: bytes.Repeat([]byte("x"), 65537), mode: "append", invalid: true},
		{name: "conflict", extra: []byte("extra"), mode: "both", invalid: true},
		{name: "missing", mode: "missing", invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			guide := filepath.Join(dir, "llms.txt")
			extra := filepath.Join(dir, "extra.txt")
			original := []byte("default\r\n")
			if err := os.WriteFile(guide, original, 0600); err != nil {
				t.Fatal(err)
			}
			if tt.mode != "missing" {
				if err := os.WriteFile(extra, tt.extra, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var appendPath, overridePath string
			switch tt.mode {
			case "append", "missing":
				appendPath = extra
			case "override":
				overridePath = extra
			case "both":
				appendPath = extra
				overridePath = extra
			}
			err := buildGuide(guide, appendPath, overridePath)
			if (err != nil) != tt.invalid {
				t.Fatalf("error=%v invalid=%v", err, tt.invalid)
			}
			got, err := os.ReadFile(guide)
			if err != nil {
				t.Fatal(err)
			}
			want := tt.want
			if tt.invalid {
				want = original
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("guide mismatch: got %d bytes, want %d", len(got), len(want))
			}
		})
	}
}
