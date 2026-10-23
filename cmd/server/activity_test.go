package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gametimesf/open-trove/storage"
)

func TestRecentViewsIncludeOwnFilesAndSites(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)
	uid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	if err := store.Put(t.Context(), "own-file", bytes.NewBufferString("hello"), storage.PutOptions{ContentType: "text/plain", Filename: "hello.txt", CustomSlug: true, Overwrite: false}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUpload(t.Context(), uid, storage.ActivityRecord{Slug: "own-file", Filename: "hello.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutSiteManifest(t.Context(), "my-site", &storage.SiteManifest{Entry: "index.html", FileCount: 1}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/own-file", "/my-site?page=details.html", "/own-file"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "trove_id", Value: uid})
		res := httptest.NewRecorder()
		e.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, res.Code)
		}
	}
	manifest, err := store.GetManifest(t.Context(), uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Views) != 2 {
		t.Fatalf("wanted own file and site, got %+v", manifest.Views)
	}
	req := httptest.NewRequest(http.MethodGet, "/mine", nil)
	req.AddCookie(&http.Cookie{Name: "trove_id", Value: uid})
	res := httptest.NewRecorder()
	e.ServeHTTP(res, req)
	recent := strings.Split(res.Body.String(), "<h2>Recently Viewed</h2>")[1]
	if first, second := strings.Index(recent, `href="/own-file"`), strings.Index(recent, `href="/my-site"`); first < 0 || second < first {
		t.Fatalf("revisited file must precede site: %s", recent)
	}
}

func TestMyTroveShowsUploaderNotViewer(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)
	uid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	owner := "owner@example.com"
	for _, slug := range []string{"file", "legacy"} {
		opts := storage.PutOptions{ContentType: "text/plain", Filename: slug + ".txt"}
		if slug == "file" {
			opts.OwnerEmail = owner
		}
		if err := store.Put(t.Context(), slug, bytes.NewBufferString("hello"), opts); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PutSiteManifest(t.Context(), "site", &storage.SiteManifest{Entry: "index.html", FileCount: 1, OwnerEmail: owner}); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"file", "site", "legacy"} {
		req := httptest.NewRequest(http.MethodGet, "/"+slug, nil)
		req.AddCookie(&http.Cookie{Name: "trove_id", Value: uid})
		req.Header.Set(troveUserEmailHeader, "viewer@example.com")
		res := httptest.NewRecorder()
		e.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s: %d", slug, res.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/mine", nil)
	req.AddCookie(&http.Cookie{Name: "trove_id", Value: uid})
	res := httptest.NewRecorder()
	e.ServeHTTP(res, req)
	html := res.Body.String()
	if strings.Count(html, "Uploaded by "+owner) != 2 || !strings.Contains(html, "Uploaded by unknown") || strings.Contains(html, "Uploaded by viewer@example.com") {
		t.Fatalf("wrong attribution: %s", html)
	}
}

func TestUploadPersistsArtifactAttribution(t *testing.T) {
	srv, store := newTestServer()
	e := newTestEcho(srv)
	req := createMultipartRequest(t, "report.txt", []byte("hello"), "owner-test")
	res := httptest.NewRecorder()
	e.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", res.Code, res.Body.String())
	}
	meta, err := store.Metadata(t.Context(), "owner-test")
	if err != nil || meta.OwnerEmail != testUserEmail {
		t.Fatalf("meta=%+v err=%v", meta, err)
	}
}

func TestDirectSiteNavigationExcludesEmbeddedAssets(t *testing.T) {
	for _, dest := range []string{"document", "iframe", "style", "empty", ""} {
		t.Run(dest, func(t *testing.T) {
			srv, store := newTestServer()
			e := newTestEcho(srv)
			if err := store.PutSiteManifest(t.Context(), "site", &storage.SiteManifest{Entry: "index.html", FileCount: 1}); err != nil {
				t.Fatal(err)
			}
			if err := store.PutSiteFile(t.Context(), "site", "index.html", bytes.NewBufferString("hello"), "text/html"); err != nil {
				t.Fatal(err)
			}
			uid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
			req := httptest.NewRequest(http.MethodGet, "/site/index.html", nil)
			req.AddCookie(&http.Cookie{Name: "trove_id", Value: uid})
			req.Header.Set("Sec-Fetch-Dest", dest)
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			res := httptest.NewRecorder()
			e.ServeHTTP(res, req)
			if res.Code != 200 {
				t.Fatalf("status %d", res.Code)
			}
			m, _ := store.GetManifest(t.Context(), uid)
			want := 0
			if dest == "document" {
				want = 1
			}
			if len(m.Views) != want {
				t.Fatalf("%s views=%d want=%d", dest, len(m.Views), want)
			}
		})
	}
}

type unavailableActivityStore struct{ storage.Store }

func (s unavailableActivityStore) GetManifest(context.Context, string) (*storage.UserManifest, error) {
	return nil, errors.New("unavailable")
}
func TestMyTroveReadFailureIsNotAnEmptyHistory(t *testing.T) {
	srv, store := newTestServer()
	srv.store = unavailableActivityStore{Store: store}
	e := newTestEcho(srv)
	res := httptest.NewRecorder()
	e.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/mine", nil))
	if res.Code != http.StatusInternalServerError || strings.Contains(res.Body.String(), "No views yet") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
func TestMyTroveUploaderIsEscaped(t *testing.T) {
	var html bytes.Buffer
	owner := `<script>alert("owner")</script>`
	err := myTroveTemplate.Execute(&html, &storage.UserManifest{Views: []storage.ActivityRecord{{Slug: "doc", Filename: "doc", OwnerEmail: owner}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), owner) || !strings.Contains(html.String(), "&lt;script&gt;") {
		t.Fatal("owner not escaped")
	}
}
