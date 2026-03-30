package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gametimesf/open-trove/storage"
	"github.com/gametimesf/open-trove/storage/fake"
	"github.com/labstack/echo/v4"
)

func newTestServer() (*server, *fake.Store) {
	store := fake.NewStore()
	srv := &server{
		store:   store,
		baseURL: "http://localhost:8080",
	}
	return srv, store
}

func newTestEcho(srv *server) *echo.Echo {
	e := echo.New()
	registerRoutes(e, srv)
	return e
}

func createMultipartRequest(t *testing.T, filename string, content []byte, slug string) *http.Request {
	t.Helper()
	return createMultipartRequestWithOverwrite(t, filename, content, slug, false)
}

func createMultipartRequestWithOverwrite(t *testing.T, filename string, content []byte, slug string, overwrite bool) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("creating form file: %v", err)
	}
	part.Write(content)

	if slug != "" {
		w.WriteField("slug", slug)
	}
	if overwrite {
		w.WriteField("overwrite", "true")
	}

	w.Close()

	req := httptest.NewRequest("POST", "/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestHandleIndex(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "trove") {
		t.Error("expected page to contain 'trove'")
	}
}

func TestHandleUploadHappyPath(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := createMultipartRequest(t, "hello.txt", []byte("hello world"), "")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["url"] == "" {
		t.Error("expected url in response")
	}
	if resp["slug"] == "" {
		t.Error("expected slug in response")
	}
}

func TestHandleUploadWithCustomSlug(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := createMultipartRequest(t, "report.html", []byte("<html>hi</html>"), "my-report")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["slug"] != "my-report" {
		t.Errorf("expected slug 'my-report', got %q", resp["slug"])
	}
	if resp["url"] != "http://localhost:8080/my-report" {
		t.Errorf("expected url 'http://localhost:8080/my-report', got %q", resp["url"])
	}
}

func TestHandleUploadInvalidSlug(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := createMultipartRequest(t, "file.txt", []byte("data"), "-bad-slug")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleUploadSlugConflict(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	// Pre-populate the store
	store.Put(nil, "taken", bytes.NewReader([]byte("x")), "text/plain", "x.txt", false, false)

	req := createMultipartRequest(t, "file.txt", []byte("data"), "taken")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 409 {
		t.Errorf("expected 409, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleUploadOverwrite(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	// Pre-populate the store
	store.Put(nil, "my-doc", bytes.NewReader([]byte("v1")), "text/plain", "old.txt", true, false)

	req := createMultipartRequestWithOverwrite(t, "new.txt", []byte("v2"), "my-doc", true)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify the file was replaced
	body, meta, err := store.Get(nil, "my-doc", "")
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	defer body.Close()
	data, _ := io.ReadAll(body)
	if string(data) != "v2" {
		t.Errorf("expected body 'v2', got %q", string(data))
	}
	if meta.Filename != "new.txt" {
		t.Errorf("expected filename 'new.txt', got %q", meta.Filename)
	}
}

func TestHandleUploadNoFile(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := httptest.NewRequest("POST", "/upload", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleViewHTML(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "page", bytes.NewReader([]byte("<html>hi</html>")), "text/html; charset=utf-8", "index.html", false, false)

	req := httptest.NewRequest("GET", "/page", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<iframe") {
		t.Error("expected iframe for HTML content")
	}
	if !strings.Contains(body, "/page/raw") {
		t.Error("expected raw URL in viewer")
	}
}

func TestHandleViewImage(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "pic", bytes.NewReader([]byte("PNG...")), "image/png", "photo.png", false, false)

	req := httptest.NewRequest("GET", "/pic", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<img") {
		t.Error("expected img tag for image content")
	}
}

func TestHandleViewOther(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "doc", bytes.NewReader([]byte("%PDF...")), "application/pdf", "report.pdf", false, false)

	req := httptest.NewRequest("GET", "/doc", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Download") {
		t.Error("expected download link for non-HTML/image content")
	}
}

func TestHandleViewNotFound(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := httptest.NewRequest("GET", "/nope", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleRaw(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	content := []byte("<html><body>Hello</body></html>")
	store.Put(nil, "raw1", bytes.NewReader(content), "text/html; charset=utf-8", "page.html", false, false)

	req := httptest.NewRequest("GET", "/raw1/raw", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("expected content-type 'text/html; charset=utf-8', got %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "page.html") {
		t.Errorf("expected content-disposition with original filename 'page.html', got %q", cd)
	}

	body, _ := io.ReadAll(w.Body)
	if !bytes.Equal(body, content) {
		t.Errorf("body mismatch: got %q", string(body))
	}
}

func TestHandleRawNotFound(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := httptest.NewRequest("GET", "/missing/raw", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestDetectContentType(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  string
		mpType   string
		want     string
	}{
		{"html by extension", "index.html", "whatever", "", "text/html; charset=utf-8"},
		{"css by extension", "style.css", "body{}", "", "text/css; charset=utf-8"},
		{"js by extension", "app.js", "var x", "", "application/javascript; charset=utf-8"},
		{"svg by extension", "icon.svg", "<svg>", "", "image/svg+xml"},
		{"json by extension", "data.json", "{}", "", "application/json; charset=utf-8"},
		{"pdf by extension", "doc.pdf", "%PDF", "", "application/pdf"},
		{"mp4 by extension", "clip.mp4", "", "", "video/mp4"},
		{"webm by extension", "clip.webm", "", "", "video/webm"},
		{"mov by extension", "clip.mov", "", "", "video/quicktime"},
		{"png by sniff", "file.unknownext123", "\x89PNG\r\n\x1a\n", "", "image/png"},
		{"fallback to multipart", "file.unknownext123", "\x00\x00", "application/zip", "application/zip"},
		{"fallback to octet-stream", "file.unknownext123", "\x00\x00", "", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.content)
			got := detectContentType(tt.filename, r, tt.mpType)
			if got != tt.want {
				t.Errorf("detectContentType(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestCookieSetOnFirstVisit(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	cookies := w.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == "trove_id" {
			found = true
			if c.Value == "" {
				t.Error("trove_id cookie should not be empty")
			}
			if !c.HttpOnly {
				t.Error("trove_id cookie should be HttpOnly")
			}
		}
	}
	if !found {
		t.Error("expected trove_id cookie to be set")
	}
}

func TestCookieReusedOnSubsequentVisit(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	// First request: get cookie
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "trove_id" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("expected trove_id cookie")
	}

	// Second request: reuse cookie
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(cookie)
	w2 := httptest.NewRecorder()
	e.ServeHTTP(w2, req2)

	// Should NOT set a new cookie
	for _, c := range w2.Result().Cookies() {
		if c.Name == "trove_id" {
			t.Error("should not set trove_id cookie when already present")
		}
	}
}

func TestHandleMineEmpty(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := httptest.NewRequest("GET", "/mine", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "My Trove") {
		t.Error("expected 'My Trove' in page")
	}
	if !strings.Contains(body, "No uploads yet") {
		t.Error("expected empty uploads state")
	}
	if !strings.Contains(body, "No views yet") {
		t.Error("expected empty views state")
	}
}

func TestUploadRecordsActivity(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	req := createMultipartRequest(t, "report.html", []byte("<html>hi</html>"), "my-report")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Extract the user ID from the cookie
	var uid string
	for _, c := range w.Result().Cookies() {
		if c.Name == "trove_id" {
			uid = c.Value
		}
	}
	if uid == "" {
		t.Fatal("expected trove_id cookie")
	}

	m, _ := store.GetManifest(nil, uid)
	if len(m.Uploads) != 1 {
		t.Fatalf("expected 1 upload, got %d", len(m.Uploads))
	}
	if m.Uploads[0].Slug != "my-report" {
		t.Errorf("expected slug 'my-report', got %q", m.Uploads[0].Slug)
	}
}

func TestViewRecordsActivity(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	userB := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	// Upload a file as user A
	store.Put(nil, "shared-doc", bytes.NewReader([]byte("hi")), "text/plain", "doc.txt", false, false)
	store.RecordUpload(nil, "user-a", storage.ActivityRecord{Slug: "shared-doc", Filename: "doc.txt", ContentType: "text/plain"})

	// View as user B
	req := httptest.NewRequest("GET", "/shared-doc", nil)
	req.AddCookie(&http.Cookie{Name: "trove_id", Value: userB})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	m, _ := store.GetManifest(nil, userB)
	if len(m.Views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(m.Views))
	}
	if m.Views[0].Slug != "shared-doc" {
		t.Errorf("expected view slug 'shared-doc', got %q", m.Views[0].Slug)
	}
}

func TestViewOwnUploadNotRecorded(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	uid := "uploader-1"
	// Upload a file and record it in manifest
	store.Put(nil, "my-file", bytes.NewReader([]byte("hi")), "text/plain", "file.txt", true, false)
	store.RecordUpload(nil, uid, storage.ActivityRecord{Slug: "my-file", Filename: "file.txt", ContentType: "text/plain"})

	// View own file
	req := httptest.NewRequest("GET", "/my-file", nil)
	req.AddCookie(&http.Cookie{Name: "trove_id", Value: uid})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	m, _ := store.GetManifest(nil, uid)
	if len(m.Views) != 0 {
		t.Errorf("expected 0 views (own upload), got %d", len(m.Views))
	}
}

func TestViewDeduplication(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "the-doc", bytes.NewReader([]byte("hi")), "text/plain", "doc.txt", false, false)

	uid := "11111111-1111-1111-1111-111111111111"

	// View twice
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/the-doc", nil)
		req.AddCookie(&http.Cookie{Name: "trove_id", Value: uid})
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
	}

	m, _ := store.GetManifest(nil, uid)
	if len(m.Views) != 1 {
		t.Errorf("expected 1 view (deduplicated), got %d", len(m.Views))
	}
}

func TestHandleRawDownloadFilename(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "my-report", bytes.NewReader([]byte("<html>hi</html>")), "text/html; charset=utf-8", "report.html", true, false)

	req := httptest.NewRequest("GET", "/my-report/raw", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "my-report.html") {
		t.Errorf("expected download filename 'my-report.html', got Content-Disposition: %q", cd)
	}
}

func TestSlugMineReserved(t *testing.T) {
	err := validateSlug("mine")
	if err == nil {
		t.Error("expected 'mine' to be reserved")
	}
}

func TestHandleMineWithData(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	uid := "22222222-2222-2222-2222-222222222222"
	store.RecordUpload(nil, uid, storage.ActivityRecord{Slug: "s1", Filename: "hello.txt", ContentType: "text/plain"})
	store.RecordView(nil, uid, storage.ActivityRecord{Slug: "s2", Filename: "world.png", ContentType: "image/png"})

	req := httptest.NewRequest("GET", "/mine", nil)
	req.AddCookie(&http.Cookie{Name: "trove_id", Value: uid})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "hello.txt") {
		t.Error("expected upload 'hello.txt' in page")
	}
	if !strings.Contains(body, "world.png") {
		t.Error("expected view 'world.png' in page")
	}
}

func TestHandleViewVideo(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "clip", bytes.NewReader([]byte{0x00, 0x00}), "video/mp4", "video.mp4", false, false)

	req := httptest.NewRequest("GET", "/clip", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<video") {
		t.Error("expected <video> element for video content")
	}
	if !strings.Contains(body, `class="video-container"`) {
		t.Error("expected video-container div")
	}
	if !strings.Contains(body, "/clip/raw") {
		t.Error("expected raw URL in video src")
	}
	if strings.Contains(body, "<iframe") {
		t.Error("video files should not use iframe")
	}
	if strings.Contains(body, `<div class="file-card">`) {
		t.Error("video files should not show download card")
	}
}

func TestViewMode(t *testing.T) {
	tests := []struct {
		contentType string
		filename    string
		want        string
	}{
		{"text/html; charset=utf-8", "index.html", "iframe"},
		{"text/html", "page.htm", "iframe"},
		{"image/png", "photo.png", "image"},
		{"image/jpeg", "pic.jpg", "image"},
		{"image/svg+xml", "icon.svg", "image"},
		{"video/mp4", "clip.mp4", "video"},
		{"video/webm", "clip.webm", "video"},
		{"video/quicktime", "clip.mov", "video"},
		{"text/csv; charset=utf-8", "data.csv", "csv"},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "report.docx", "docx"},
		{"text/markdown; charset=utf-8", "README.md", "markdown"},
		{"text/plain; charset=utf-8", "main.go", "code"},
		{"text/plain; charset=utf-8", "script.py", "code"},
		{"application/json; charset=utf-8", "data.json", "code"},
		{"text/plain; charset=utf-8", "Makefile", "code"},
		{"application/pdf", "report.pdf", "download"},
		{"application/octet-stream", "data.bin", "download"},
		{"application/zip", "archive.zip", "download"},
		// plain text with no highlight lang still falls through to download
		{"text/plain; charset=utf-8", "file.unknownext", "download"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := viewMode(tt.contentType, tt.filename)
			if got != tt.want {
				t.Errorf("viewMode(%q, %q) = %q, want %q", tt.contentType, tt.filename, got, tt.want)
			}
		})
	}
}

func TestHighlightLang(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"main.go", "go"},
		{"script.py", "python"},
		{"app.js", "javascript"},
		{"index.ts", "typescript"},
		{"component.jsx", "javascript"},
		{"component.tsx", "typescript"},
		{"data.json", "json"},
		{"style.css", "css"},
		{"page.html", "html"},
		{"page.htm", "html"},
		{"README.md", "markdown"},
		{"config.yaml", "yaml"},
		{"config.yml", "yaml"},
		{"config.toml", "toml"},
		{"run.sh", "bash"},
		{"run.bash", "bash"},
		{"run.zsh", "bash"},
		{"query.sql", "sql"},
		{"lib.rs", "rust"},
		{"App.java", "java"},
		{"app.rb", "ruby"},
		{"index.php", "php"},
		{"main.c", "c"},
		{"header.h", "c"},
		{"main.cpp", "cpp"},
		{"header.hpp", "cpp"},
		{"Program.cs", "csharp"},
		{"main.swift", "swift"},
		{"Main.kt", "kotlin"},
		{"Main.scala", "scala"},
		{"analysis.r", "r"},
		{"script.lua", "lua"},
		{"script.pl", "perl"},
		{"main.tf", "hcl"},
		{"config.hcl", "hcl"},
		{"data.csv", ""},
		{"notes.txt", "plaintext"},
		{"output.log", "plaintext"},
		{".env", "plaintext"},
		{"config.ini", "ini"},
		{"nginx.conf", "ini"},
		{"photo.png", ""},
		{"report.pdf", ""},
		{"archive.zip", ""},
		{"Makefile", "makefile"},
		{"Dockerfile", "dockerfile"},
		{"MAKEFILE", "makefile"},
		{"DOCKERFILE", "dockerfile"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := highlightLang(tt.filename)
			if got != tt.want {
				t.Errorf("highlightLang(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestHandleViewCode(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "code1", bytes.NewReader([]byte("package main\n")), "text/plain; charset=utf-8", "main.go", false, false)

	req := httptest.NewRequest("GET", "/code1", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `language-go`) {
		t.Error("expected language-go class in code block")
	}
	if !strings.Contains(body, "highlight.min.js") {
		t.Error("expected highlight.js script tag")
	}
	if strings.Contains(body, "<iframe") {
		t.Error("code files should not use iframe")
	}
	if !strings.Contains(body, `<div class="code-container">`) {
		t.Error("expected code-container div")
	}
}

func TestHandleViewCodePython(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "pyfile", bytes.NewReader([]byte("print('hello')\n")), "text/plain; charset=utf-8", "script.py", false, false)

	req := httptest.NewRequest("GET", "/pyfile", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `language-python`) {
		t.Error("expected language-python class")
	}
}

func TestHandleViewCSV(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "csvfile", bytes.NewReader([]byte("a,b,c\n1,2,3\n")), "text/csv; charset=utf-8", "data.csv", false, false)

	req := httptest.NewRequest("GET", "/csvfile", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `csv-container`) {
		t.Error("expected csv-container for CSV files")
	}
	if !strings.Contains(body, `id="csv-table"`) {
		t.Error("expected csv-table element")
	}
	if !strings.Contains(body, "papaparse") {
		t.Error("expected PapaParse CDN script")
	}
	if strings.Contains(body, "highlight.min.js") {
		t.Error("CSV should not use highlight.js")
	}
	if strings.Contains(body, `<div class="code-container">`) {
		t.Error("CSV should not use code-container")
	}
	if strings.Contains(body, "<iframe") {
		t.Error("CSV should not use iframe")
	}
}

func TestHandleViewHTMLNotCode(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "htmlpage", bytes.NewReader([]byte("<html><body>Hello</body></html>")), "text/html; charset=utf-8", "index.html", false, false)

	req := httptest.NewRequest("GET", "/htmlpage", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<iframe") {
		t.Error("HTML files should still use iframe")
	}
	if strings.Contains(body, "highlight.min.js") {
		t.Error("HTML files should not include highlight.js")
	}
	if strings.Contains(body, `<div class="code-container">`) {
		t.Error("HTML files should not have code-container")
	}
}

func TestHandleViewOGTags(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "og-test", bytes.NewReader([]byte("hello")), "text/plain; charset=utf-8", "hello.txt", false, false)

	req := httptest.NewRequest("GET", "/og-test", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `og:title`) {
		t.Error("expected og:title meta tag")
	}
	if !strings.Contains(body, `content="hello.txt"`) {
		t.Error("expected og:title to contain filename")
	}
	if !strings.Contains(body, `og:site_name`) {
		t.Error("expected og:site_name meta tag")
	}
	if !strings.Contains(body, `content="trove"`) {
		t.Error("expected og:site_name to be 'trove'")
	}
	if !strings.Contains(body, `og:url`) {
		t.Error("expected og:url meta tag")
	}
	if !strings.Contains(body, `content="http://localhost:8080/og-test"`) {
		t.Error("expected og:url to contain full URL")
	}
}

func TestHandleViewOGTagsOnImage(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "ogimg", bytes.NewReader([]byte("PNG...")), "image/png", "photo.png", false, false)

	req := httptest.NewRequest("GET", "/ogimg", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `og:title`) {
		t.Error("expected og:title on image viewer too")
	}
	if !strings.Contains(body, `content="photo.png"`) {
		t.Error("expected og:title to contain image filename")
	}
}

func TestHandleViewImageStillWorks(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "img1", bytes.NewReader([]byte("PNG...")), "image/png", "photo.png", false, false)

	req := httptest.NewRequest("GET", "/img1", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "<img") {
		t.Error("expected img tag for image")
	}
	if strings.Contains(body, `<div class="code-container">`) {
		t.Error("images should not have code-container")
	}
	if strings.Contains(body, "highlight.min.js") {
		t.Error("images should not include highlight.js")
	}
}

func TestHandleViewDownloadFallback(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "binfile", bytes.NewReader([]byte{0x00, 0x01}), "application/octet-stream", "data.bin", false, false)

	req := httptest.NewRequest("GET", "/binfile", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "file-card") {
		t.Error("binary files should show download card")
	}
	if strings.Contains(body, `<div class="code-container">`) {
		t.Error("binary files should not have code-container")
	}
}

func TestHandleViewDocx(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "docfile", bytes.NewReader([]byte("PK\x03\x04...")), "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "report.docx", false, false)

	req := httptest.NewRequest("GET", "/docfile", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `docx-container`) {
		t.Error("expected docx-container for .docx files")
	}
	if !strings.Contains(body, "docx-preview") {
		t.Error("expected docx-preview CDN script")
	}
	if !strings.Contains(body, `id="docx-wrapper"`) {
		t.Error("expected docx-wrapper element")
	}
	if strings.Contains(body, "<iframe") {
		t.Error("docx should not use iframe")
	}
	if strings.Contains(body, "highlight.min.js") {
		t.Error("docx should not use highlight.js")
	}
	if strings.Contains(body, `<div class="code-container">`) {
		t.Error("docx should not use code-container")
	}
}

func TestHandleViewDocxNotCode(t *testing.T) {
	// Verify .docx doesn't fall through to code viewer
	srv, store := newTestServer()
	e := newTestEcho(srv)

	store.Put(nil, "doc2", bytes.NewReader([]byte("PK...")), "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "notes.docx", false, false)

	req := httptest.NewRequest("GET", "/doc2", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, `<div class="file-card">`) {
		t.Error("docx should not show download card fallback")
	}
}

func TestDetectContentTypeDocx(t *testing.T) {
	r := strings.NewReader("PK\x03\x04")
	got := detectContentType("report.docx", r, "")
	want := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	if got != want {
		t.Errorf("detectContentType(report.docx) = %q, want %q", got, want)
	}
}

func TestHandleAgentJSON(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := httptest.NewRequest("GET", "/.well-known/agent.json", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected application/json, got %q", ct)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["name"] != "trove" {
		t.Errorf("expected name 'trove', got %v", resp["name"])
	}
	endpoints, ok := resp["endpoints"].([]any)
	if !ok || len(endpoints) == 0 {
		t.Fatal("expected at least one endpoint")
	}
	ep := endpoints[0].(map[string]any)
	if ep["method"] != "POST" {
		t.Errorf("expected method POST, got %v", ep["method"])
	}
	if ep["path"] != "/upload" {
		t.Errorf("expected path /upload, got %v", ep["path"])
	}
}

func TestHandleLLMsTxt(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := httptest.NewRequest("GET", "/llms.txt", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain, got %q", ct)
	}
	body := w.Body.String()
	for _, want := range []string{"# Trove", "POST /upload", "GET /{slug}", "curl"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected body to contain %q", want)
		}
	}
	if !strings.Contains(body, "http://localhost:8080") {
		t.Errorf("expected body to contain base URL")
	}
}

func TestHandleDelete(t *testing.T) {
	srv, store := newTestServer()
	e := echo.New()
	e.DELETE("/delete/:slug", srv.handleDelete)

	// Seed a file
	store.Put(t.Context(), "del-me", strings.NewReader("bye"), "text/plain", "bye.txt", true, false)

	req := httptest.NewRequest("DELETE", "/delete/del-me", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 204 {
		t.Errorf("expected 204, got %d", w.Code)
	}

	// Confirm it's gone
	_, err := store.Metadata(t.Context(), "del-me")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestHandleDeleteNotFound(t *testing.T) {
	srv, _ := newTestServer()
	e := echo.New()
	e.DELETE("/delete/:slug", srv.handleDelete)

	req := httptest.NewRequest("DELETE", "/delete/nonexistent", nil)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
