package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCommentAssetsAreServed(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	tests := []struct {
		path        string
		contentType string
		contains    []string
	}{
		{
			path:        "/_trove/comments.js",
			contentType: "text/javascript",
			contains: []string{
				"data-trove-id",
				"getSelection",
				"/api/artifacts/",
				"trove:resource-change",
				"currentTarget.element.contains(element)",
				"/resolution",
				"Write a reply",
				`item.addEventListener("click"`,
				`event.target.closest("button, a, input, textarea, select, form")`,
				"Collapse thread",
				"trove-comment-thread__summary",
			},
		},
		{
			path:        "/_trove/comments.css",
			contentType: "text/css",
			contains: []string{
				".trove-comments-drawer",
				".trove-comments-open",
				"grid-template-columns",
				".trove-comment-thread__summary",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); !strings.Contains(got, tt.contentType) {
				t.Fatalf("Content-Type = %q, want %q", got, tt.contentType)
			}
			for _, want := range tt.contains {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("asset does not contain %q", want)
				}
			}
		})
	}
}

func TestEveryViewerRendersCommentChrome(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		contentType string
		data        string
	}{
		{name: "HTML", filename: "report.html", contentType: "text/html; charset=utf-8", data: "<h1>Report</h1>"},
		{name: "text", filename: "report.txt", contentType: "text/plain; charset=utf-8", data: "Report"},
		{name: "image", filename: "image.png", contentType: "image/png", data: "not-a-real-image"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, store := newTestServer()
			if err := store.Put(context.Background(), "artifact", bytes.NewBufferString(tt.data), tt.contentType, tt.filename, true, false); err != nil {
				t.Fatalf("Put() error = %v", err)
			}
			e := newTestEcho(srv)
			req := httptest.NewRequest(http.MethodGet, "/artifact", nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			for _, want := range []string{
				`id="trove-comments-toggle"`,
				`id="trove-comments-drawer"`,
				`id="trove-comments-list"`,
				`id="trove-comment-form"`,
				`id="trove-comments-show-resolved"`,
				`data-trove-slug="artifact"`,
				`/_trove/comments.css`,
				`/_trove/comments.js`,
			} {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("viewer does not contain %q", want)
				}
			}
		})
	}
}

func TestSiteViewerRendersCommentChrome(t *testing.T) {
	srv, store := newTestServer()
	if err := store.PutSiteFile(context.Background(), "site", "index.html", bytes.NewBufferString("<h1>Home</h1>"), "text/html; charset=utf-8"); err != nil {
		t.Fatalf("PutSiteFile() error = %v", err)
	}
	e := newTestEcho(srv)
	req := httptest.NewRequest(http.MethodGet, "/site", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		`id="trove-comments-toggle"`,
		`id="trove-comments-drawer"`,
		`data-trove-resource="index.html"`,
		`trove:resource-change`,
		`id="trove-comments-show-resolved"`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("site viewer does not contain %q", want)
		}
	}
}
