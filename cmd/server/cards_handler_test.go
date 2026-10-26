package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gametimesf/open-trove/storage"
)

func pngOfSize(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func cardRequest(t *testing.T, slug string, image []byte, alt string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if image != nil {
		part, err := w.CreateFormFile("image", "card.png")
		if err != nil {
			t.Fatal(err)
		}
		part.Write(image)
	}
	if alt != "" {
		w.WriteField("alt", alt)
	}
	w.Close()
	req := httptest.NewRequest(http.MethodPut, "/api/artifacts/"+slug+"/preview", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set(troveUserEmailHeader, testUserEmail)
	return req
}

func TestCardLifecycle(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)
	ctx := t.Context()
	if err := store.Put(ctx, "q3-review", strings.NewReader("<h1>Q3</h1>"), storage.PutOptions{ContentType: "text/html", Filename: "q3.html", OwnerEmail: testUserEmail}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, cardRequest(t, "q3-review", pngOfSize(t, 1200, 630), "Q3 at a glance"))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d: %s", rec.Code, rec.Body.String())
	}
	var got cardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Width != 1200 || got.Height != 630 || got.Alt != "Q3 at a glance" || got.UpdatedByEmail != testUserEmail ||
		got.ArtifactVersion == "" || got.URL != "http://localhost:8080/api/artifacts/q3-review/preview" {
		t.Fatalf("response = %+v", got)
	}

	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/artifacts/q3-review/preview", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("ETag") != `"`+got.SHA256+`"` {
		t.Fatalf("GET status = %d headers = %v", rec.Code, rec.Header())
	}
	if _, err := png.Decode(rec.Body); err != nil {
		t.Fatalf("GET body is not a PNG: %v", err)
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/artifacts/q3-review/preview", nil)
	del.Header.Set(troveUserEmailHeader, testUserEmail)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, del)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/artifacts/q3-review/preview", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after delete = %d", rec.Code)
	}
}

func TestPutCardErrors(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)
	if err := store.Put(t.Context(), "doc", strings.NewReader("x"), storage.PutOptions{ContentType: "text/plain", Filename: "doc.txt"}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		slug string
		img  []byte
		want int
	}{
		{name: "unknown artifact", slug: "missing", img: pngOfSize(t, 200, 200), want: http.StatusNotFound},
		{name: "internal slug", slug: "_tokens", img: pngOfSize(t, 200, 200), want: http.StatusNotFound},
		{name: "missing image", slug: "doc", want: http.StatusBadRequest},
		{name: "not an image", slug: "doc", img: []byte("<svg/>"), want: http.StatusBadRequest},
		{name: "too small", slug: "doc", img: pngOfSize(t, 50, 50), want: http.StatusBadRequest},
		{name: "too large", slug: "doc", img: make([]byte, 3<<20), want: http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, cardRequest(t, tt.slug, tt.img, ""))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestCardForSiteAndArtifactDelete(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)
	ctx := t.Context()
	if err := store.PutSiteFile(ctx, "handbook", "index.html", strings.NewReader("<h1>Handbook</h1>"), "text/html"); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSiteManifest(ctx, "handbook", &storage.SiteManifest{Entry: "index.html", FileCount: 1}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, cardRequest(t, "handbook", pngOfSize(t, 300, 200), ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT on site = %d: %s", rec.Code, rec.Body.String())
	}

	if err := store.Put(ctx, "doc", strings.NewReader("x"), storage.PutOptions{ContentType: "text/plain", Filename: "doc.txt"}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, cardRequest(t, "doc", pngOfSize(t, 300, 200), ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d", rec.Code)
	}
	del := httptest.NewRequest(http.MethodDelete, "/delete/doc", nil)
	del.Header.Set(troveUserEmailHeader, testUserEmail)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, del)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("artifact delete = %d", rec.Code)
	}
	if _, err := store.GetCard(ctx, "doc"); err == nil {
		t.Fatal("card survived artifact deletion")
	}
}
