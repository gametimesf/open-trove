package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	trovedocs "github.com/gametimesf/open-trove/docs"
)

func TestLLMSTxtDefaultAndOverride(t *testing.T) {
	embedded, err := trovedocs.Files.ReadFile("llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	const custom = "  # Deployment guide\r\nCafé → diagrams\n\n"
	for _, override := range []*string{nil, stringPointerForLLMS(custom)} {
		name, want := "embedded", string(embedded)
		if override != nil {
			name, want = "override", *override
		}
		t.Run(name, func(t *testing.T) {
			srv, _ := newTestServer()
			srv.llmsTxtOverride = override
			e := newTestEcho(srv)
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				t.Run(method, func(t *testing.T) {
					w := httptest.NewRecorder()
					e.ServeHTTP(w, httptest.NewRequest(method, "/llms.txt", nil))
					if w.Code != http.StatusOK {
						t.Fatalf("status = %d", w.Code)
					}
					if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
						t.Fatalf("content type = %q", w.Header().Get("Content-Type"))
					}
					if got := w.Header().Get("Content-Length"); got != strconv.Itoa(len(want)) {
						t.Fatalf("content length = %s, want %d", got, len(want))
					}
					wantBody := want
					if method == http.MethodHead {
						wantBody = ""
					}
					if w.Body.String() != wantBody {
						t.Fatalf("body = %q, want %q", w.Body.String(), wantBody)
					}
				})
			}
		})
	}
}

func TestLLMSTxtOverrideIsSnapshotAtRegistration(t *testing.T) {
	content := "# Original\n"
	srv, _ := newTestServer()
	srv.llmsTxtOverride = &content
	ts := httptest.NewServer(newTestEcho(srv))
	defer ts.Close()
	content = "# Changed after startup\n"
	resp, err := ts.Client().Get(ts.URL + "/llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "# Original\n" {
		t.Fatalf("body changed after registration: %q", body)
	}
}

func stringPointerForLLMS(s string) *string { return &s }
