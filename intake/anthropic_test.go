package intake

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeAnthropic returns a test server that asserts the request shape and
// responds with a tool_use block carrying the supplied verdict.
func fakeAnthropic(t *testing.T, want func(*messagesRequest), verdict Verdict) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing api key header: %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != anthropicVersion {
			t.Errorf("missing version: %q", r.Header.Get("anthropic-version"))
		}
		body, _ := io.ReadAll(r.Body)
		var got messagesRequest
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if want != nil {
			want(&got)
		}
		input, _ := json.Marshal(verdict)
		resp := messagesResponse{
			Type: "message",
			Role: "assistant",
			Content: []contentBlock{
				{Type: "tool_use", Name: "record_verdict", Input: input},
			},
			StopReason: "tool_use",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func newInspector(t *testing.T, server *httptest.Server) *AnthropicInspector {
	t.Helper()
	insp, err := NewAnthropic(AnthropicConfig{
		APIKey:   "test-key",
		Model:    "claude-test",
		Endpoint: server.URL,
		Guidance: "test guidance",
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	return insp
}

func TestNewAnthropicRequiresAPIKey(t *testing.T) {
	if _, err := NewAnthropic(AnthropicConfig{}); err == nil {
		t.Error("expected error without API key")
	}
}

func TestNewAnthropicDefaults(t *testing.T) {
	insp, err := NewAnthropic(AnthropicConfig{APIKey: "k", Guidance: "review policy"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if insp.cfg.Model == "" {
		t.Error("expected default model")
	}
	if insp.cfg.MaxBytes == 0 {
		t.Error("expected default max bytes")
	}
	if insp.cfg.Guidance != "review policy" {
		t.Error("expected configured guidance")
	}
}

func TestNewAnthropicRequiresGuidance(t *testing.T) {
	if _, err := NewAnthropic(AnthropicConfig{APIKey: "k"}); err == nil {
		t.Error("expected error without guidance or prompt source")
	}
}

func TestInspectTextAllowed(t *testing.T) {
	server := fakeAnthropic(t, func(req *messagesRequest) {
		if len(req.Messages) != 1 {
			t.Errorf("expected one message, got %d", len(req.Messages))
		}
		c := req.Messages[0].Content[0]
		if c.Type != "text" {
			t.Errorf("expected text block, got %q", c.Type)
		}
		if !strings.Contains(c.Text, "hello world") {
			t.Errorf("body not in prompt: %q", c.Text)
		}
		if req.System != "test guidance" {
			t.Errorf("system mismatch: %q", req.System)
		}
		if req.ToolChoice == nil || req.ToolChoice.Name != "record_verdict" {
			t.Errorf("tool_choice not forced: %+v", req.ToolChoice)
		}
	}, Verdict{Allowed: true})
	defer server.Close()

	v, err := newInspector(t, server).Inspect(context.Background(), Input{
		Filename:    "notes.md",
		ContentType: "text/markdown",
		Body:        []byte("hello world"),
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !v.Allowed {
		t.Error("expected allowed")
	}
}

func TestInspectBlockedReturnsReason(t *testing.T) {
	server := fakeAnthropic(t, nil, Verdict{
		Allowed:    false,
		Reason:     "looks like compensation data",
		Categories: []string{"hr"},
	})
	defer server.Close()

	v, err := newInspector(t, server).Inspect(context.Background(), Input{
		Filename:    "salaries.csv",
		ContentType: "text/csv",
		Body:        []byte("name,salary\nA,100k"),
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if v.Allowed {
		t.Error("expected blocked")
	}
	if v.Reason == "" || len(v.Categories) == 0 {
		t.Errorf("missing reason/categories: %+v", v)
	}
}

func TestInspectHTMLConvertsToMarkdown(t *testing.T) {
	server := fakeAnthropic(t, func(req *messagesRequest) {
		c := req.Messages[0].Content[0]
		if c.Type != "text" {
			t.Errorf("expected text block, got %q", c.Type)
		}
		// We expect the converter to have stripped tags into markdown.
		if strings.Contains(c.Text, "<h1>") {
			t.Errorf("HTML not converted to markdown: %q", c.Text)
		}
		if !strings.Contains(c.Text, "# Title") {
			t.Errorf("missing markdown heading: %q", c.Text)
		}
	}, Verdict{Allowed: true})
	defer server.Close()

	_, err := newInspector(t, server).Inspect(context.Background(), Input{
		Filename:    "page.html",
		ContentType: "text/html",
		Body:        []byte("<html><body><h1>Title</h1><p>Body</p></body></html>"),
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
}

func TestInspectImageVision(t *testing.T) {
	server := fakeAnthropic(t, func(req *messagesRequest) {
		c := req.Messages[0].Content[0]
		if c.Type != "image" {
			t.Errorf("expected image block, got %q", c.Type)
		}
		if c.Source == nil || c.Source.MediaType != "image/png" {
			t.Errorf("source mismatch: %+v", c.Source)
		}
		if c.Source.Type != "base64" {
			t.Errorf("expected base64 source, got %q", c.Source.Type)
		}
	}, Verdict{Allowed: true})
	defer server.Close()

	v, err := newInspector(t, server).Inspect(context.Background(), Input{
		Filename:    "photo.png",
		ContentType: "image/png",
		Body:        []byte("\x89PNG\r\n\x1a\nfake"),
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !v.Allowed {
		t.Error("expected allowed")
	}
}

func TestInspectPDFDocument(t *testing.T) {
	server := fakeAnthropic(t, func(req *messagesRequest) {
		c := req.Messages[0].Content[0]
		if c.Type != "document" {
			t.Errorf("expected document block, got %q", c.Type)
		}
		if c.Source == nil || c.Source.MediaType != "application/pdf" {
			t.Errorf("source mismatch: %+v", c.Source)
		}
	}, Verdict{Allowed: true})
	defer server.Close()

	_, err := newInspector(t, server).Inspect(context.Background(), Input{
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		Body:        []byte("%PDF-1.4..."),
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
}

func TestInspectSkipsBypassed(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cases := []Input{
		{Filename: "demo.mp4", ContentType: "video/mp4", Body: []byte("MP4")},
		{Filename: "tone.mp3", ContentType: "audio/mpeg", Body: []byte("MP3")},
		{Filename: "site.zip", ContentType: "application/zip", Body: []byte("PK")},
		{Filename: "doc.docx", ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Body: []byte("DOCX")},
	}
	insp := newInspector(t, server)
	for _, in := range cases {
		v, err := insp.Inspect(context.Background(), in)
		if err != nil {
			t.Errorf("%s: err %v", in.Filename, err)
		}
		if v == nil || !v.Allowed {
			t.Errorf("%s: expected allowed, got %+v", in.Filename, v)
		}
	}
	if called {
		t.Error("bypass types must not call the API")
	}
}

func TestInspectAPIErrorReturnsErrInspector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer server.Close()

	_, err := newInspector(t, server).Inspect(context.Background(), Input{
		Filename:    "a.md",
		ContentType: "text/markdown",
		Body:        []byte("hi"),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInspector) {
		t.Errorf("expected ErrInspector, got %v", err)
	}
}

func TestInspectMissingToolUseReturnsErr(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messagesResponse{
			Content:    []contentBlock{{Type: "text", Text: "I refuse to use the tool"}},
			StopReason: "end_turn",
		})
	}))
	defer server.Close()

	_, err := newInspector(t, server).Inspect(context.Background(), Input{
		Filename: "a.md", ContentType: "text/markdown", Body: []byte("hi"),
	})
	if err == nil || !errors.Is(err, ErrInspector) {
		t.Errorf("expected ErrInspector, got %v", err)
	}
}

func TestInspectTruncatesOversizedBody(t *testing.T) {
	server := fakeAnthropic(t, func(req *messagesRequest) {
		c := req.Messages[0].Content[0]
		if !strings.Contains(c.Text, "[truncated;") {
			t.Errorf("expected truncation marker in: %q", c.Text[:200])
		}
	}, Verdict{Allowed: true})
	defer server.Close()

	insp, _ := NewAnthropic(AnthropicConfig{
		APIKey:   "test-key",
		Endpoint: server.URL,
		Guidance: "test",
		MaxBytes: 32,
	})
	big := strings.Repeat("x", 1024)
	if _, err := insp.Inspect(context.Background(), Input{
		Filename: "big.txt", ContentType: "text/plain", Body: []byte(big),
	}); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
}
