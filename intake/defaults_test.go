package intake

import (
	"errors"
	"testing"
)

func TestGuidanceInlineWins(t *testing.T) {
	got := Guidance("inline-rules", "/some/path", func(string) ([]byte, error) {
		t.Error("readFile should not be called when inline is set")
		return nil, nil
	})
	if got != "inline-rules" {
		t.Errorf("got %q", got)
	}
}

func TestGuidancePathReads(t *testing.T) {
	got := Guidance("", "/some/path", func(p string) ([]byte, error) {
		if p != "/some/path" {
			t.Errorf("readFile got %q", p)
		}
		return []byte("from-file"), nil
	})
	if got != "from-file" {
		t.Errorf("got %q", got)
	}
}

func TestGuidancePathFailureReturnsEmpty(t *testing.T) {
	got := Guidance("", "/missing", func(string) ([]byte, error) {
		return nil, errors.New("no")
	})
	if got != "" {
		t.Errorf("path read failure returned %q", got)
	}
}

func TestGuidanceRequiresOperatorInput(t *testing.T) {
	got := Guidance("", "", nil)
	if got != "" {
		t.Errorf("expected no built-in guidance, got %q", got)
	}
}
