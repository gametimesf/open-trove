package intake

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown"
)

// Anthropic Messages API endpoint and version pin. Hardcoded so the
// integration is reproducible across deploys.
const (
	anthropicURL     = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
)

// AnthropicConfig configures the inspector.
//
// Provide either Guidance (a static string, simplest path for tests
// and local dev) or PromptSource (consulted on every Inspect, enables
// refresh-without-restart in production). When both are set,
// PromptSource wins.
type AnthropicConfig struct {
	APIKey       string
	Model        string        // e.g. "claude-sonnet-4-6"
	Guidance     string        // static guidance — wins iff PromptSource is nil
	PromptSource PromptSource  // dynamic guidance — read on every Inspect
	MaxBytes     int           // truncate input bodies above this size before sending
	Timeout      time.Duration // per-request timeout
	Endpoint     string        // overrides the production URL (tests, proxies)
	HTTPClient   *http.Client  // override for tests
}

// AnthropicInspector implements Inspector against the Anthropic Messages API.
type AnthropicInspector struct {
	cfg  AnthropicConfig
	http *http.Client
}

// NewAnthropic constructs an Anthropic-backed Inspector.
func NewAnthropic(cfg AnthropicConfig) (*AnthropicInspector, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("intake: anthropic api key required")
	}
	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-6"
	}
	if cfg.Guidance == "" && cfg.PromptSource == nil {
		return nil, fmt.Errorf("intake: guidance or prompt source required")
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 200 * 1024
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	c := cfg.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: cfg.Timeout}
	}
	return &AnthropicInspector{cfg: cfg, http: c}, nil
}

// currentGuidance returns the active guidance string for this Inspect
// call. When a PromptSource is configured, it's read on every call
// (the source itself is responsible for caching). Failures fall back
// to the static Guidance.
func (a *AnthropicInspector) currentGuidance(ctx context.Context) string {
	if a.cfg.PromptSource != nil {
		raw, err := a.cfg.PromptSource.Fetch(ctx)
		if err == nil && len(raw) > 0 {
			return string(raw)
		}
		// Source failed; warn loudly and fall through.
		// (Caller — typically the gate — already gets ErrInspector
		// via the outer flow; this branch only runs when the source
		// fails on a per-call refresh, which CachedSource normally
		// shields with last-good-value semantics.)
	}
	if a.cfg.Guidance != "" {
		return a.cfg.Guidance
	}
	return ""
}

// Inspect runs the configured guidance over the input and returns a Verdict.
func (a *AnthropicInspector) Inspect(ctx context.Context, in Input) (*Verdict, error) {
	block, skip, err := a.buildContent(in)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInspector, err)
	}
	if skip {
		return &Verdict{Allowed: true}, nil
	}

	reqBody := a.buildRequest(ctx, in.Filename, block)
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal: %v", ErrInspector, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint(), bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: new request: %v", ErrInspector, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.cfg.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: http: %v", ErrInspector, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("%w: status %d: %s", ErrInspector, resp.StatusCode, string(body))
	}

	var out messagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrInspector, err)
	}
	verdict, err := extractVerdict(out)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInspector, err)
	}
	return verdict, nil
}

// endpoint returns the configured URL (tests pass a httptest server URL).
func (a *AnthropicInspector) endpoint() string {
	if a.cfg.Endpoint != "" {
		return a.cfg.Endpoint
	}
	return anthropicURL
}

// buildContent classifies the input and produces the right content
// block to send. skip=true means we deliberately bypass inspection
// (allowed types we choose not to scan).
func (a *AnthropicInspector) buildContent(in Input) (contentBlock, bool, error) {
	ct := strings.ToLower(in.ContentType)
	ext := strings.ToLower(filepath.Ext(in.Filename))

	// Skip categories.
	switch {
	case strings.HasPrefix(ct, "video/"),
		strings.HasPrefix(ct, "audio/"),
		strings.Contains(ct, "zip"),
		strings.Contains(ct, "wordprocessingml"),
		ext == ".docx", ext == ".zip":
		return contentBlock{}, true, nil
	}

	// Image vision.
	if strings.HasPrefix(ct, "image/") {
		mediaType := canonicalImageType(ct, ext)
		if mediaType == "" {
			// Unsupported image; skip rather than fail.
			return contentBlock{}, true, nil
		}
		return contentBlock{
			Type: "image",
			Source: &contentSource{
				Type:      "base64",
				MediaType: mediaType,
				Data:      base64.StdEncoding.EncodeToString(in.Body),
			},
		}, false, nil
	}

	// PDF document block.
	if strings.HasPrefix(ct, "application/pdf") || ext == ".pdf" {
		return contentBlock{
			Type: "document",
			Source: &contentSource{
				Type:      "base64",
				MediaType: "application/pdf",
				Data:      base64.StdEncoding.EncodeToString(in.Body),
			},
		}, false, nil
	}

	// Text path: HTML → markdown, otherwise raw.
	body := in.Body
	if strings.HasPrefix(ct, "text/html") || ext == ".html" || ext == ".htm" {
		md, err := htmltomd.NewConverter("", true, nil).ConvertString(string(body))
		if err == nil {
			body = []byte(md)
		}
	}

	if a.cfg.MaxBytes > 0 && len(body) > a.cfg.MaxBytes {
		body = append(body[:a.cfg.MaxBytes:a.cfg.MaxBytes], []byte("\n\n[truncated; original was larger than the inspector window]")...)
	}

	text := fmt.Sprintf("Filename: %s\nContent-Type: %s\n\n--- BEGIN CONTENT ---\n%s\n--- END CONTENT ---", in.Filename, in.ContentType, string(body))
	return contentBlock{Type: "text", Text: text}, false, nil
}

// canonicalImageType maps content-types Anthropic accepts. Returns ""
// for unsupported types.
func canonicalImageType(ct, ext string) string {
	ct = strings.ToLower(strings.SplitN(ct, ";", 2)[0])
	switch ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return ct
	}
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// buildRequest assembles the Messages API call. We force structured
// output via tool_use — the model is required to call our single
// `record_verdict` tool.
func (a *AnthropicInspector) buildRequest(ctx context.Context, filename string, block contentBlock) messagesRequest {
	return messagesRequest{
		Model:     a.cfg.Model,
		MaxTokens: 1024,
		System:    a.currentGuidance(ctx),
		ToolChoice: &toolChoice{
			Type: "tool",
			Name: "record_verdict",
		},
		Tools: []tool{
			{
				Name:        "record_verdict",
				Description: "Record the verdict for this input.",
				InputSchema: verdictSchema(),
			},
		},
		Messages: []message{
			{
				Role:    "user",
				Content: []contentBlock{block},
			},
		},
	}
}

// extractVerdict pulls the tool_use input out of the response.
func extractVerdict(resp messagesResponse) (*Verdict, error) {
	for _, c := range resp.Content {
		if c.Type == "tool_use" && c.Name == "record_verdict" {
			var v Verdict
			if err := json.Unmarshal(c.Input, &v); err != nil {
				return nil, fmt.Errorf("decoding verdict: %w", err)
			}
			return &v, nil
		}
	}
	return nil, errors.New("no tool_use record_verdict in response")
}

// verdictSchema is the JSON schema for the `record_verdict` tool input.
func verdictSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"allowed":    map[string]any{"type": "boolean"},
			"reason":     map[string]any{"type": "string"},
			"categories": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"allowed"},
	}
}
