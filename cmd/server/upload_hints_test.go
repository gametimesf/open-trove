package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gametimesf/open-trove/internal/config"
	"github.com/gametimesf/open-trove/preview"
)

func hintCodes(hints []preview.Hint) []string {
	out := []string{}
	for _, h := range hints {
		out = append(out, h.Code)
	}
	return out
}

func TestUploadHints(t *testing.T) {
	bare := []byte(`<h1>Q3</h1><p>A paragraph long enough to stand in for a description.</p>`)
	annotated := []byte(`<title>Q3</title><meta name="description" content="Beat plan."><meta name="trove:stat" content="GMV: $1M">`)
	tests := []struct {
		name     string
		filename string
		content  []byte
		client   string
		withCard bool
		want     []string
	}{
		{name: "bare page", filename: "q3.html", content: bare, want: []string{preview.HintTitle, preview.HintDescription, preview.HintStats, preview.HintCard}},
		{name: "annotated page without card", filename: "q3.html", content: annotated, want: []string{preview.HintCard}},
		{name: "annotated page with card", filename: "q3.html", content: annotated, withCard: true, want: []string{}},
		{name: "outdated skill", filename: "q3.html", content: annotated, withCard: true, client: "trove-skill/1.2.0", want: []string{HintClientUpdate}},
		{name: "current skill", filename: "q3.html", content: annotated, withCard: true, client: "trove-skill/1.3.0", want: []string{}},
		{name: "unknown client", filename: "q3.html", content: annotated, withCard: true, client: "curl/8.0", want: []string{}},
		{name: "CSV", filename: "data.csv", content: []byte("a,b\n1,2\n"), want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, store := newTestServer()
			srv.latestClients = map[string]config.ClientRelease{"trove-skill": {Version: "1.3.0", Update: "Run /plugin and update trove."}}
			e := newTestEcho(srv)
			if tt.withCard {
				if err := store.PutCard(t.Context(), "q3", preview.Card{SHA256: strings.Repeat("a", 64)}); err != nil {
					t.Fatal(err)
				}
			}
			var buf bytes.Buffer
			w := multipart.NewWriter(&buf)
			part, _ := w.CreateFormFile("file", tt.filename)
			part.Write(tt.content)
			w.WriteField("slug", "q3")
			w.Close()
			req := httptest.NewRequest(http.MethodPost, "/upload", &buf)
			req.Header.Set("Content-Type", w.FormDataContentType())
			req.Header.Set(troveUserEmailHeader, testUserEmail)
			if tt.client != "" {
				req.Header.Set(troveClientHeader, tt.client)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
			}
			resp := decode[UploadResponse](t, rec)
			if got := hintCodes(resp.Hints); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("hints = %v, want %v", got, tt.want)
			}
			for _, h := range resp.Hints {
				if strings.Contains(h.Message, "{slug}") {
					t.Errorf("unsubstituted slug in %q", h.Message)
				}
				if h.Code == preview.HintCard && !strings.Contains(h.Message, "/api/artifacts/q3/preview") {
					t.Errorf("card hint should name this artifact: %q", h.Message)
				}
				if h.Code == HintClientUpdate && !strings.Contains(h.Message, "1.3.0") || h.Code == HintClientUpdate && !strings.Contains(h.Message, "/plugin") {
					t.Errorf("client hint = %q", h.Message)
				}
			}
		})
	}
}

func TestSiteUploadHints(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)
	rec := uploadForm(t, e, "site.zip", createZIP(t, map[string][]byte{"index.html": []byte("<title>Docs</title><h2>Setup</h2>")}), map[string]string{"slug": "docs"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("site upload = %d: %s", rec.Code, rec.Body.String())
	}
	want := []string{preview.HintDescription, preview.HintStats, preview.HintCard}
	if got := hintCodes(decode[UploadResponse](t, rec).Hints); !reflect.DeepEqual(got, want) {
		t.Fatalf("site hints = %v, want %v", got, want)
	}
}
