package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/labstack/echo/v4"
)

// uploadForm posts /upload with optional slug, if_match and If-Match header.
func uploadForm(t *testing.T, e *echo.Echo, filename string, content []byte, fields map[string]string, ifMatchHeader string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(content)
	for k, v := range fields {
		w.WriteField(k, v)
	}
	w.Close()
	req := httptest.NewRequest(http.MethodPost, "/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set(troveUserEmailHeader, testUserEmail)
	if ifMatchHeader != "" {
		req.Header.Set("If-Match", ifMatchHeader)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return v
}

func TestConditionalFileOverwrite(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	first := uploadForm(t, e, "report.html", []byte("<p>v1</p>"), map[string]string{"slug": "report"}, "")
	if first.Code != http.StatusOK {
		t.Fatalf("create = %d: %s", first.Code, first.Body.String())
	}
	v1 := decode[UploadResponse](t, first).Version
	if v1 == "" {
		t.Fatal("upload response has no version")
	}

	meta := httptest.NewRecorder()
	e.ServeHTTP(meta, httptest.NewRequest(http.MethodGet, "/api/artifacts/report", nil))
	if a := decode[ArtifactResponse](t, meta); meta.Code != http.StatusOK || a.Version != v1 || a.Kind != "file" || meta.Header().Get("ETag") != strconv.Quote(v1) {
		t.Fatalf("metadata = %d %+v etag=%q", meta.Code, a, meta.Header().Get("ETag"))
	}
	raw := httptest.NewRecorder()
	e.ServeHTTP(raw, httptest.NewRequest(http.MethodGet, "/report/raw", nil))
	if raw.Header().Get("ETag") != strconv.Quote(v1) {
		t.Fatalf("raw ETag = %q, want %q", raw.Header().Get("ETag"), v1)
	}

	second := uploadForm(t, e, "report.html", []byte("<p>v2</p>"), map[string]string{"slug": "report", "if_match": v1}, "")
	if second.Code != http.StatusOK {
		t.Fatalf("overwrite with current version = %d: %s", second.Code, second.Body.String())
	}
	v2 := decode[UploadResponse](t, second).Version
	if v2 == "" || v2 == v1 {
		t.Fatalf("version after overwrite = %q (was %q)", v2, v1)
	}

	stale := uploadForm(t, e, "report.html", []byte("<p>stale</p>"), map[string]string{"slug": "report"}, `"`+v1+`"`)
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match = %d, want 412: %s", stale.Code, stale.Body.String())
	}
	if c := decode[VersionConflictResponse](t, stale); c.CurrentVersion != v2 {
		t.Fatalf("conflict current_version = %q, want %q", c.CurrentVersion, v2)
	}
	body := httptest.NewRecorder()
	e.ServeHTTP(body, httptest.NewRequest(http.MethodGet, "/report/raw", nil))
	if body.Body.String() != "<p>v2</p>" {
		t.Fatalf("content after rejected write = %q", body.Body.String())
	}

	tests := []struct {
		name   string
		fields map[string]string
		want   int
	}{
		{name: "if_match needs a slug", fields: map[string]string{"if_match": v2}, want: http.StatusBadRequest},
		{name: "if_match on a missing artifact", fields: map[string]string{"slug": "missing", "if_match": v2}, want: http.StatusPreconditionFailed},
		{name: "plain overwrite still works", fields: map[string]string{"slug": "report", "overwrite": "true"}, want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := uploadForm(t, e, "report.html", []byte("<p>x</p>"), tt.fields, ""); rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestConditionalSiteOverwrite(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)
	zipV1 := createZIP(t, map[string][]byte{"index.html": []byte("<h1>v1</h1>")})
	zipV2 := createZIP(t, map[string][]byte{"index.html": []byte("<h1>v2</h1>")})

	first := uploadForm(t, e, "site.zip", zipV1, map[string]string{"slug": "handbook"}, "")
	if first.Code != http.StatusOK {
		t.Fatalf("site create = %d: %s", first.Code, first.Body.String())
	}
	up := decode[UploadResponse](t, first)
	if up.Type != "site" || up.Version == "" {
		t.Fatalf("site upload response = %+v", up)
	}
	meta := httptest.NewRecorder()
	e.ServeHTTP(meta, httptest.NewRequest(http.MethodGet, "/api/artifacts/handbook", nil))
	if a := decode[ArtifactResponse](t, meta); a.Kind != "site" || a.Version != up.Version || a.FileCount != 1 {
		t.Fatalf("site metadata = %+v", a)
	}

	if rec := uploadForm(t, e, "site.zip", zipV2, map[string]string{"slug": "handbook", "if_match": "stale"}, ""); rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale site if_match = %d", rec.Code)
	}
	if rec := uploadForm(t, e, "site.zip", zipV2, map[string]string{"slug": "handbook", "if_match": up.Version}, ""); rec.Code != http.StatusOK {
		t.Fatalf("site overwrite with current version = %d: %s", rec.Code, rec.Body.String())
	}
	m, err := store.GetSiteManifest(t.Context(), "handbook")
	if err != nil || m.Version == up.Version || m.Version == "" {
		t.Fatalf("manifest version after overwrite = %q (%v)", m.Version, err)
	}
}

func TestGetArtifactNotFound(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)
	for _, slug := range []string{"missing", "_tokens"} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/artifacts/"+slug, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET /api/artifacts/%s = %d, want 404", slug, rec.Code)
		}
	}
}
