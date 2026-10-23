package intake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectFileNilInspector(t *testing.T) {
	if _, err := InspectFile(context.Background(), nil, "anything"); err == nil {
		t.Error("expected error with nil inspector")
	}
}

func TestInspectFileMissingFile(t *testing.T) {
	if _, err := InspectFile(context.Background(), NoOp{}, "/no/such/file/here"); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestInspectFileReadsAndCallsInspector(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.md")
	if err := os.WriteFile(path, []byte("# hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	captured := struct {
		filename, ct string
		body         []byte
	}{}
	insp := inspectorFunc(func(_ context.Context, in Input) (*Verdict, error) {
		captured.filename = in.Filename
		captured.ct = in.ContentType
		captured.body = in.Body
		return &Verdict{Allowed: false, Reason: "nope"}, nil
	})
	v, err := InspectFile(context.Background(), insp, path)
	if err != nil {
		t.Fatalf("InspectFile: %v", err)
	}
	if v.Allowed {
		t.Error("expected blocked")
	}
	if captured.filename != "report.md" {
		t.Errorf("filename = %q", captured.filename)
	}
	if captured.ct != "text/markdown; charset=utf-8" {
		t.Errorf("content type = %q", captured.ct)
	}
	if string(captured.body) != "# hello" {
		t.Errorf("body = %q", captured.body)
	}
}

func TestInspectFilePropagatesError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.md")
	_ = os.WriteFile(path, []byte("x"), 0o644)
	insp := inspectorFunc(func(_ context.Context, _ Input) (*Verdict, error) {
		return nil, errors.New("boom")
	})
	if _, err := InspectFile(context.Background(), insp, path); err == nil {
		t.Error("expected error to propagate")
	}
}

// inspectorFunc adapts a function literal to the Inspector interface.
type inspectorFunc func(context.Context, Input) (*Verdict, error)

func (f inspectorFunc) Inspect(ctx context.Context, in Input) (*Verdict, error) {
	return f(ctx, in)
}
