package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gametimesf/open-trove/intake"
)

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func createUploadRequest(t *testing.T, filename string, content []byte, slug string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("createFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("part write: %v", err)
	}
	if slug != "" {
		_ = w.WriteField("slug", slug)
	}
	w.Close()
	req := httptest.NewRequest("POST", "/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set(troveUserEmailHeader, testUserEmail)
	return req
}

// fakeInspector is a tiny stub used by handler tests so we don't have to
// stand up an HTTP server. The behavior is configured per test.
type fakeInspector struct {
	verdict *intake.Verdict
	err     error
	calls   int
}

func (f *fakeInspector) Inspect(_ context.Context, _ intake.Input) (*intake.Verdict, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.verdict, nil
}

func TestUploadAllowedPassesThrough(t *testing.T) {
	srv, _ := newTestServer()
	insp := &fakeInspector{verdict: &intake.Verdict{Allowed: true}}
	srv.intake = insp
	e := newTestEcho(srv)

	req := createMultipartRequest(t, "report.md", []byte("# hi"), "")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if insp.calls != 1 {
		t.Errorf("inspector calls = %d", insp.calls)
	}
}

func TestUploadBlockedReturns403WithReason(t *testing.T) {
	srv, store := newTestServer()
	srv.intake = &fakeInspector{verdict: &intake.Verdict{
		Allowed:    false,
		Reason:     "looks like compensation data",
		Categories: []string{"hr"},
	}}
	e := newTestEcho(srv)

	req := createMultipartRequest(t, "salaries.csv", []byte("name,salary\nA,100k"), "")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "compensation data") {
		t.Errorf("body missing reason: %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"hr"`) {
		t.Errorf("body missing category: %q", w.Body.String())
	}
	// Importantly, nothing should have been written to the store.
	if _, err := store.Metadata(context.Background(), "salaries"); err == nil {
		t.Error("blocked upload was persisted")
	}
}

func TestUploadInspectorErrorFailClosed(t *testing.T) {
	srv, _ := newTestServer()
	srv.intake = &fakeInspector{err: errors.New("boom")}
	srv.intakeFail = intake.FailClosed
	e := newTestEcho(srv)

	req := createMultipartRequest(t, "x.md", []byte("hi"), "")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 503 {
		t.Errorf("expected 503 (fail-closed), got %d: %s", w.Code, w.Body.String())
	}
}

func TestUploadInspectorErrorFailOpen(t *testing.T) {
	srv, _ := newTestServer()
	srv.intake = &fakeInspector{err: errors.New("boom")}
	srv.intakeFail = intake.FailOpen
	e := newTestEcho(srv)

	req := createMultipartRequest(t, "x.md", []byte("hi"), "")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200 (fail-open allows), got %d: %s", w.Code, w.Body.String())
	}
}

func TestUploadNoOpAlwaysAllows(t *testing.T) {
	srv, _ := newTestServer() // already uses NoOp
	e := newTestEcho(srv)

	req := createMultipartRequest(t, "anything.md", []byte("anything at all"), "")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200 with NoOp, got %d", w.Code)
	}
}

func TestSiteUploadInspectorBlocksFirstViolatingFile(t *testing.T) {
	srv, store := newTestServer()
	// Block any file whose body contains the marker "BLOCKME".
	srv.intake = inspectorFunc(func(_ context.Context, in intake.Input) (*intake.Verdict, error) {
		if strings.Contains(string(in.Body), "BLOCKME") {
			return &intake.Verdict{Allowed: false, Reason: "test marker"}, nil
		}
		return &intake.Verdict{Allowed: true}, nil
	})
	e := newTestEcho(srv)

	zipBytes := buildZip(t, map[string]string{
		"index.html": "<html>fine</html>",
		"page2.html": "<html>BLOCKME</html>",
	})
	req := createUploadRequest(t, "site.zip", zipBytes, "test-site")

	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "test marker") {
		t.Errorf("body missing reason: %q", w.Body.String())
	}
	// Site upload should have been aborted — no manifest persisted.
	if exists, _ := store.HeadSite(context.Background(), "test-site"); exists {
		t.Error("blocked site upload was persisted")
	}
}

// inspectorFunc adapts a function to the Inspector interface.
type inspectorFunc func(context.Context, intake.Input) (*intake.Verdict, error)

func (f inspectorFunc) Inspect(ctx context.Context, in intake.Input) (*intake.Verdict, error) {
	return f(ctx, in)
}

func TestInternalSlugsAreNotServed(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	// Direct write to the store under an internal-prefix key, simulating
	// what an admin's prompt upload (or a legacy _users entry) would
	// look like to the view path.
	if err := srv.store.Put(context.Background(), "_prompt", strings.NewReader("super secret guidance"), "text/plain", "_prompt", false, true); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []string{"/_prompt", "/_prompt/raw", "/_prompt/anything"}
	for _, path := range cases {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		if w.Code != 404 {
			t.Errorf("%s: expected 404, got %d (body: %s)", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "super secret") {
			t.Errorf("%s: prompt content leaked: %q", path, w.Body.String())
		}
	}
}
