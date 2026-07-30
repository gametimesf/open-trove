package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gametimesf/open-trove/comments"
)

func TestCommentAPIExistingSingleFile(t *testing.T) {
	srv, store := newTestServer()
	if err := store.Put(context.Background(), "report", bytes.NewBufferString("<h1>Report</h1>"), "text/html; charset=utf-8", "report.html", true, false); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	e := newTestEcho(srv)

	body := `{
		"body": "Make the title clearer",
		"anchor": {
			"type": "element",
			"stable_id": "report-title",
			"selector": "[data-trove-id=\"report-title\"]",
			"label": "Report title"
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/artifacts/report/comments", bytes.NewBufferString(body))
	req.Header.Set(echoHeaderContentType, "application/json")
	req.Header.Set(troveUserEmailHeader, testUserEmail)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created comments.Comment
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding comment: %v", err)
	}
	if created.AuthorEmail != testUserEmail || created.Slug != "report" {
		t.Fatalf("created comment = %#v", created)
	}
	if created.ArtifactVersion == "" {
		t.Fatal("created comment has no artifact version")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/artifacts/report/comments", nil)
	req.Header.Set(troveUserEmailHeader, testUserEmail)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var listed CommentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding comments: %v", err)
	}
	if len(listed.Threads) != 1 || listed.Threads[0].Root.ID != created.ID || listed.OpenThreadCount != 1 {
		t.Fatalf("listed threads = %#v", listed.Threads)
	}
	if listed.CurrentVersion != created.ArtifactVersion {
		t.Fatalf("current version = %q, created version = %q", listed.CurrentVersion, created.ArtifactVersion)
	}
}

func TestSingleFileCommentsIgnoreResourcePath(t *testing.T) {
	srv, store := newTestServer()
	if err := store.Put(context.Background(), "report", bytes.NewBufferString("report"), "text/plain", "report.txt", true, false); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	e := newTestEcho(srv)

	created := commentRequest(
		t,
		e,
		http.MethodPost,
		"/api/artifacts/report/comments?path=index.html",
		`{"body":"Whole-file comment"}`,
		testUserEmail,
		http.StatusCreated,
	)
	if created.Anchor.Resource != "" {
		t.Fatalf("Anchor.Resource = %q, want whole-file resource", created.Anchor.Resource)
	}

	listed := listThreads(t, e, "/api/artifacts/report/comments?path=index.html")
	if len(listed.Threads) != 1 || listed.Threads[0].Root.ID != created.ID {
		t.Fatalf("List() = %#v, want created whole-file comment", listed.Threads)
	}
}

func TestCommentAPIExistingSitePage(t *testing.T) {
	srv, store := newTestServer()
	if err := store.PutSiteFile(context.Background(), "site", "index.html", bytes.NewBufferString("<h1>Home</h1>"), "text/html; charset=utf-8"); err != nil {
		t.Fatalf("PutSiteFile() error = %v", err)
	}
	if err := store.PutSiteManifest(context.Background(), "site", nil); err != nil {
		t.Fatalf("PutSiteManifest() error = %v", err)
	}
	e := newTestEcho(srv)

	body := `{"body":"Change this text","anchor":{"type":"text","resource":"index.html","quote":{"exact":"Home"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/artifacts/site/comments", bytes.NewBufferString(body))
	req.Header.Set(echoHeaderContentType, "application/json")
	req.Header.Set(troveUserEmailHeader, testUserEmail)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/artifacts/site/comments?path=index.html", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var listed CommentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding comments: %v", err)
	}
	if len(listed.Threads) != 1 || listed.Resource != "index.html" {
		t.Fatalf("listed = %#v", listed)
	}
}

func TestSiteCommentMutationsUseRequestedPage(t *testing.T) {
	srv, store := newTestServer()
	if err := store.PutSiteFile(context.Background(), "site", "pages/report.html", bytes.NewBufferString("<h1>Report</h1>"), "text/html; charset=utf-8"); err != nil {
		t.Fatalf("PutSiteFile() error = %v", err)
	}
	if err := store.PutSiteManifest(context.Background(), "site", nil); err != nil {
		t.Fatalf("PutSiteManifest() error = %v", err)
	}
	e := newTestEcho(srv)

	root := commentRequest(
		t,
		e,
		http.MethodPost,
		"/api/artifacts/site/comments",
		`{"body":"Review this","anchor":{"type":"element","resource":"pages/report.html","selector":"h1"}}`,
		testUserEmail,
		http.StatusCreated,
	)
	commentRequest(
		t,
		e,
		http.MethodPatch,
		"/api/artifacts/site/comments/"+root.ID+"?path=pages/report.html",
		`{"body":"Review this title"}`,
		testUserEmail,
		http.StatusOK,
	)
	commentRequest(
		t,
		e,
		http.MethodPatch,
		"/api/artifacts/site/comments/"+root.ID+"/resolution?path=pages/report.html",
		`{"resolved":true}`,
		testUserEmail,
		http.StatusOK,
	)

	req := httptest.NewRequest(http.MethodDelete, "/api/artifacts/site/comments/"+root.ID+"?path=pages/report.html", nil)
	req.Header.Set(troveUserEmailHeader, testUserEmail)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCommentThreadLifecycleAPI(t *testing.T) {
	srv, store := newTestServer()
	if err := store.Put(context.Background(), "report", bytes.NewBufferString("report"), "text/plain", "report.txt", true, false); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	e := newTestEcho(srv)

	root := commentRequest(t, e, http.MethodPost, "/api/artifacts/report/comments", `{"body":"Root comment"}`, testUserEmail, http.StatusCreated)
	reply := commentRequest(
		t,
		e,
		http.MethodPost,
		"/api/artifacts/report/comments/"+root.ID+"/replies",
		`{"body":"First reply"}`,
		"reply@example.com",
		http.StatusCreated,
	)
	if reply.ParentID != root.ID || reply.ThreadID != root.ID {
		t.Fatalf("reply = %#v", reply)
	}

	_ = commentRequest(
		t,
		e,
		http.MethodPatch,
		"/api/artifacts/report/comments/"+reply.ID,
		`{"body":"Edited reply"}`,
		"reply@example.com",
		http.StatusOK,
	)
	commentRequest(
		t,
		e,
		http.MethodPatch,
		"/api/artifacts/report/comments/"+reply.ID,
		`{"body":"Forbidden edit"}`,
		testUserEmail,
		http.StatusForbidden,
	)

	resolved := commentRequest(
		t,
		e,
		http.MethodPatch,
		"/api/artifacts/report/comments/"+root.ID+"/resolution",
		`{"resolved":true}`,
		testUserEmail,
		http.StatusOK,
	)
	if resolved.ResolvedAt == nil {
		t.Fatal("resolved root has no resolved_at")
	}

	listed := listThreads(t, e, "/api/artifacts/report/comments")
	if len(listed.Threads) != 0 {
		t.Fatalf("default list includes resolved threads: %#v", listed.Threads)
	}
	listed = listThreads(t, e, "/api/artifacts/report/comments?resolved=include")
	if len(listed.Threads) != 1 || len(listed.Threads[0].Replies) != 1 || listed.Threads[0].Replies[0].Body != "Edited reply" {
		t.Fatalf("resolved list = %#v", listed.Threads)
	}

	commentRequest(
		t,
		e,
		http.MethodPatch,
		"/api/artifacts/report/comments/"+root.ID+"/resolution",
		`{"resolved":false}`,
		testUserEmail,
		http.StatusOK,
	)
	commentRequest(
		t,
		e,
		http.MethodDelete,
		"/api/artifacts/report/comments/"+reply.ID,
		"",
		"reply@example.com",
		http.StatusNoContent,
	)
	listed = listThreads(t, e, "/api/artifacts/report/comments")
	if len(listed.Threads) != 1 || len(listed.Threads[0].Replies) != 0 {
		t.Fatalf("list after reply delete = %#v", listed.Threads)
	}

	commentRequest(
		t,
		e,
		http.MethodDelete,
		"/api/artifacts/report/comments/"+root.ID,
		"",
		testUserEmail,
		http.StatusNoContent,
	)
	listed = listThreads(t, e, "/api/artifacts/report/comments?resolved=include")
	if len(listed.Threads) != 0 {
		t.Fatalf("deleted empty thread remains visible: %#v", listed.Threads)
	}
}

func TestCommentAPIErrors(t *testing.T) {
	srv, store := newTestServer()
	if err := store.Put(context.Background(), "report", bytes.NewBufferString("report"), "text/plain", "report.txt", true, false); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	e := newTestEcho(srv)

	tests := []struct {
		name       string
		method     string
		target     string
		body       string
		email      string
		wantStatus int
	}{
		{name: "missing artifact", method: http.MethodGet, target: "/api/artifacts/missing/comments", wantStatus: http.StatusNotFound},
		{name: "write identity required", method: http.MethodPost, target: "/api/artifacts/report/comments", body: `{"body":"hello"}`, wantStatus: http.StatusBadRequest},
		{name: "invalid comment", method: http.MethodPost, target: "/api/artifacts/report/comments", body: `{"body":""}`, email: testUserEmail, wantStatus: http.StatusBadRequest},
		{name: "unknown json field", method: http.MethodPost, target: "/api/artifacts/report/comments", body: `{"body":"hello","author_email":"spoof@example.com"}`, email: testUserEmail, wantStatus: http.StatusBadRequest},
		{name: "invalid resolution filter", method: http.MethodGet, target: "/api/artifacts/report/comments?resolved=yes", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, bytes.NewBufferString(tt.body))
			if tt.body != "" {
				req.Header.Set(echoHeaderContentType, "application/json")
			}
			if tt.email != "" {
				req.Header.Set(troveUserEmailHeader, tt.email)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestDeleteArtifactRemovesComments(t *testing.T) {
	srv, store := newTestServer()
	if err := store.Put(context.Background(), "report", bytes.NewBufferString("report"), "text/plain", "report.txt", true, false); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	e := newTestEcho(srv)

	req := httptest.NewRequest(http.MethodPost, "/api/artifacts/report/comments", bytes.NewBufferString(`{"body":"Remove with artifact"}`))
	req.Header.Set(echoHeaderContentType, "application/json")
	req.Header.Set(troveUserEmailHeader, testUserEmail)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/delete/report", nil)
	req.Header.Set(troveUserEmailHeader, testUserEmail)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if err := store.Put(context.Background(), "report", bytes.NewBufferString("replacement"), "text/plain", "report.txt", true, false); err != nil {
		t.Fatalf("replacement Put() error = %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/artifacts/report/comments", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var listed CommentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding comments: %v", err)
	}
	if len(listed.Threads) != 0 {
		t.Fatalf("comments survived artifact deletion: %#v", listed.Threads)
	}
}

func commentRequest(
	t *testing.T,
	e http.Handler,
	method, target, body, email string,
	wantStatus int,
) comments.Comment {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set(echoHeaderContentType, "application/json")
	}
	if email != "" {
		req.Header.Set(troveUserEmailHeader, email)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body = %s", method, target, rec.Code, wantStatus, rec.Body.String())
	}
	if rec.Code == http.StatusNoContent {
		return comments.Comment{}
	}
	var comment comments.Comment
	if err := json.Unmarshal(rec.Body.Bytes(), &comment); err != nil {
		t.Fatalf("decoding %s %s: %v", method, target, err)
	}
	return comment
}

func listThreads(t *testing.T, e http.Handler, target string) CommentListResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set(troveUserEmailHeader, testUserEmail)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, body = %s", target, rec.Code, rec.Body.String())
	}
	var listed CommentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding GET %s: %v", target, err)
	}
	return listed
}

const echoHeaderContentType = "Content-Type"
