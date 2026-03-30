//go:build integration

package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/*
var testdata embed.FS

var baseURL string

func TestMain(m *testing.M) {
	// When BASE_URL is set (e.g. inside docker-compose), skip compose lifecycle.
	// Otherwise, start compose ourselves for local `make integration-test` usage.
	managedCompose := false
	if u := os.Getenv("BASE_URL"); u != "" {
		baseURL = u
	} else {
		managedCompose = true
		port := os.Getenv("HOST_PORT")
		if port == "" {
			port = "8080"
		}
		baseURL = fmt.Sprintf("http://localhost:%s", port)

		up := exec.Command("docker", "compose", "up", "-d", "--build", "--wait", "app")
		up.Stdout = os.Stdout
		up.Stderr = os.Stderr
		if err := up.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "docker compose up failed: %v\n", err)
			os.Exit(1)
		}
	}

	// Wait for healthz
	if err := waitForHealth(baseURL+"/healthz", 30*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "health check failed: %v\n", err)
		if managedCompose {
			teardown()
		}
		os.Exit(1)
	}

	code := m.Run()
	if managedCompose {
		teardown()
	}
	os.Exit(code)
}

func teardown() {
	down := exec.Command("docker", "compose", "down", "-v")
	down.Stdout = os.Stdout
	down.Stderr = os.Stderr
	down.Run()
}

func waitForHealth(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("health check timed out after %s", timeout)
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := testdata.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return data
}

func uploadFile(t *testing.T, client *http.Client, filename string, content []byte, slug string, overwrite bool) map[string]string {
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

	resp, err := client.Post(baseURL+"/upload", w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("POST /upload status %d: %s", resp.StatusCode, body)
	}

	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decoding upload response: %v", err)
	}
	return result
}

func TestHealthz(t *testing.T) {
	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "ok" {
		t.Errorf("expected status ok, got %q", result["status"])
	}
}

func TestLLMsTxt(t *testing.T) {
	resp, err := http.Get(baseURL + "/llms.txt")
	if err != nil {
		t.Fatalf("GET /llms.txt: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"# Trove", "POST /upload", "GET /{slug}"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("expected body to contain %q", want)
		}
	}
}

func TestAgentJSON(t *testing.T) {
	resp, err := http.Get(baseURL + "/.well-known/agent.json")
	if err != nil {
		t.Fatalf("GET /.well-known/agent.json: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if result["name"] != "trove" {
		t.Errorf("expected name 'trove', got %v", result["name"])
	}
}

func TestUploadAndView(t *testing.T) {
	client := newClient()

	// Upload a text file
	result := uploadFile(t, client, "hello.txt", fixture(t, "hello.txt"), "", false)
	slug := result["slug"]
	if slug == "" {
		t.Fatal("expected slug in upload response")
	}
	if result["url"] == "" {
		t.Fatal("expected url in upload response")
	}

	// View it via the slug
	viewURL := baseURL + "/" + slug
	resp, err := client.Get(viewURL)
	if err != nil {
		t.Fatalf("GET /%s: %v", slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Raw download
	resp, err = client.Get(baseURL + "/" + slug + "/raw")
	if err != nil {
		t.Fatalf("GET /%s/raw: %v", slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for raw, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello world") {
		t.Errorf("expected raw body to contain 'hello world', got %q", string(body))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain content type, got %q", ct)
	}
}

func TestUploadCustomSlug(t *testing.T) {
	client := newClient()

	result := uploadFile(t, client, "page.html", fixture(t, "page.html"), "integration-test-slug", false)
	if result["slug"] != "integration-test-slug" {
		t.Errorf("expected custom slug, got %q", result["slug"])
	}

	// Verify it's accessible at the custom slug
	resp, err := client.Get(baseURL + "/integration-test-slug/raw")
	if err != nil {
		t.Fatalf("GET /integration-test-slug/raw: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<h1>Hello</h1>") {
		t.Errorf("expected html body, got %q", string(body))
	}
}

func TestSlugConflict(t *testing.T) {
	client := newClient()

	uploadFile(t, client, "hello.txt", fixture(t, "hello.txt"), "conflict-test", false)

	// Second upload with same slug should 409
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "hello.txt")
	part.Write(fixture(t, "hello.txt"))
	w.WriteField("slug", "conflict-test")
	w.Close()

	resp, err := client.Post(baseURL+"/upload", w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 409 {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 409, got %d: %s", resp.StatusCode, body)
	}
}

func TestOverwrite(t *testing.T) {
	client := newClient()

	uploadFile(t, client, "hello.txt", fixture(t, "hello.txt"), "overwrite-test", false)
	uploadFile(t, client, "data.json", fixture(t, "data.json"), "overwrite-test", true)

	// Verify the content was replaced
	resp, err := client.Get(baseURL + "/overwrite-test/raw")
	if err != nil {
		t.Fatalf("GET /overwrite-test/raw: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "trove") {
		t.Errorf("expected json content after overwrite, got %q", string(body))
	}
}

func TestNotFound(t *testing.T) {
	resp, err := http.Get(baseURL + "/nonexistent-slug-12345")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestDelete(t *testing.T) {
	client := newClient()
	uploadFile(t, client, "hello.txt", fixture(t, "hello.txt"), "delete-test", false)

	// Delete it
	req, _ := http.NewRequest("DELETE", baseURL+"/delete/delete-test", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /delete-test: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}

	// Confirm it's gone
	resp, err = client.Get(baseURL + "/delete-test/raw")
	if err != nil {
		t.Fatalf("GET after delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

func TestDeleteNotFound(t *testing.T) {
	req, _ := http.NewRequest("DELETE", baseURL+"/delete/nonexistent-delete-test", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestMineTracksUploads(t *testing.T) {
	client := newClient()

	// Upload a file to establish the cookie
	uploadFile(t, client, "readme.md", fixture(t, "readme.md"), "mine-test-upload", false)

	// Check /mine contains the upload
	resp, err := client.Get(baseURL + "/mine")
	if err != nil {
		t.Fatalf("GET /mine: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "readme.md") {
		t.Error("expected /mine to contain 'readme.md'")
	}
}

func TestMineTracksViews(t *testing.T) {
	uploader := newClient()
	viewer := newClient()

	// User A uploads
	uploadFile(t, uploader, "hello.txt", fixture(t, "hello.txt"), "mine-view-test", false)

	// User B views
	resp, err := viewer.Get(baseURL + "/mine-view-test")
	if err != nil {
		t.Fatalf("GET /mine-view-test: %v", err)
	}
	resp.Body.Close()

	// User B checks /mine — should see the view
	resp, err = viewer.Get(baseURL + "/mine")
	if err != nil {
		t.Fatalf("GET /mine: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hello.txt") {
		t.Error("expected viewer's /mine to contain 'hello.txt'")
	}
}

func TestContentTypePreserved(t *testing.T) {
	client := newClient()

	tests := []struct {
		filename    string
		slug        string
		wantContain string
	}{
		{"hello.txt", "ct-txt", "text/plain"},
		{"page.html", "ct-html", "text/html"},
		{"readme.md", "ct-md", "text/markdown"},
		{"data.json", "ct-json", "application/json"},
		{"data.csv", "ct-csv", "text/csv"},
		{"main.go", "ct-go", "text/plain"},
		{"style.css", "ct-css", "text/css"},
		{"app.js", "ct-js", "javascript"},
		{"photo.png", "ct-png", "image/png"},
		{"doc.pdf", "ct-pdf", "application/pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			uploadFile(t, client, tt.filename, fixture(t, tt.filename), tt.slug, false)

			resp, err := client.Get(baseURL + "/" + tt.slug + "/raw")
			if err != nil {
				t.Fatalf("GET /%s/raw: %v", tt.slug, err)
			}
			defer resp.Body.Close()

			ct := resp.Header.Get("Content-Type")
			if !strings.Contains(ct, tt.wantContain) {
				t.Errorf("expected Content-Type containing %q, got %q", tt.wantContain, ct)
			}
		})
	}
}

// ─── MCP Protocol Tests ──────────────────────────────────────────

type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func mcpInit(t *testing.T) string {
	t.Helper()
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":   map[string]any{},
			"clientInfo":     map[string]any{"name": "integration-test", "version": "1.0"},
		},
	}
	body, _ := json.Marshal(req)
	resp, err := http.Post(baseURL+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /mcp initialize: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("MCP initialize status %d", resp.StatusCode)
	}

	sid := resp.Header.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("expected Mcp-Session-Id header")
	}

	var rpcResp jsonrpcResponse
	json.NewDecoder(resp.Body).Decode(&rpcResp)
	if rpcResp.Error != nil {
		t.Fatalf("MCP initialize error: %s", rpcResp.Error.Message)
	}
	return sid
}

func mcpCall(t *testing.T, sid string, id int, method string, params any) jsonrpcResponse {
	t.Helper()
	req := jsonrpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	body, _ := json.Marshal(req)

	httpReq, _ := http.NewRequest("POST", baseURL+"/mcp", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Mcp-Session-Id", sid)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("POST /mcp %s: %v", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("MCP %s status %d: %s", method, resp.StatusCode, respBody)
	}

	var rpcResp jsonrpcResponse
	json.NewDecoder(resp.Body).Decode(&rpcResp)
	return rpcResp
}

func TestMCPInitialize(t *testing.T) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":   map[string]any{},
			"clientInfo":     map[string]any{"name": "integration-test", "version": "1.0"},
		},
	}
	body, _ := json.Marshal(req)
	resp, err := http.Post(baseURL+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid == "" {
		t.Error("expected Mcp-Session-Id header")
	}

	var rpcResp jsonrpcResponse
	json.NewDecoder(resp.Body).Decode(&rpcResp)
	if rpcResp.Error != nil {
		t.Fatalf("unexpected error: %s", rpcResp.Error.Message)
	}

	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Capabilities struct {
			Tools map[string]any `json:"tools"`
		} `json:"capabilities"`
	}
	json.Unmarshal(rpcResp.Result, &result)

	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("expected protocol 2024-11-05, got %q", result.ProtocolVersion)
	}
	if result.ServerInfo.Name == "" {
		t.Error("expected server name")
	}
}

func TestMCPListTools(t *testing.T) {
	sid := mcpInit(t)
	resp := mcpCall(t, sid, 2, "tools/list", map[string]any{})

	if resp.Error != nil {
		t.Fatalf("tools/list error: %s", resp.Error.Message)
	}

	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Type string `json:"type"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	json.Unmarshal(resp.Result, &result)

	if len(result.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}

	toolNames := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		toolNames[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
		if tool.InputSchema.Type != "object" {
			t.Errorf("tool %q schema type = %q, want object", tool.Name, tool.InputSchema.Type)
		}
	}

	for _, want := range []string{"POST_upload", "GET_slug", "GET_slug_raw", "GET_llmstxt", "GET_mine"} {
		if !toolNames[want] {
			t.Errorf("expected tool %q in tools/list", want)
		}
	}
}

func TestMCPCallTool(t *testing.T) {
	sid := mcpInit(t)

	resp := mcpCall(t, sid, 2, "tools/call", map[string]any{
		"name":      "GET_llmstxt",
		"arguments": map[string]any{},
	})

	if resp.Error != nil {
		t.Fatalf("tools/call error: %s", resp.Error.Message)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	json.Unmarshal(resp.Result, &result)

	if len(result.Content) == 0 {
		t.Fatal("expected content in tool response")
	}
	if result.Content[0].Type != "text" {
		t.Errorf("expected content type 'text', got %q", result.Content[0].Type)
	}
	if !strings.Contains(result.Content[0].Text, "# Trove") {
		t.Error("expected llms.txt content to contain '# Trove'")
	}
}

func TestMCPCallToolGetSlug(t *testing.T) {
	client := newClient()
	uploadFile(t, client, "readme.md", fixture(t, "readme.md"), "mcp-slug-test", false)

	sid := mcpInit(t)
	resp := mcpCall(t, sid, 2, "tools/call", map[string]any{
		"name":      "GET_slug_raw",
		"arguments": map[string]any{"slug": "mcp-slug-test"},
	})

	if resp.Error != nil {
		t.Fatalf("tools/call error: %s", resp.Error.Message)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	json.Unmarshal(resp.Result, &result)

	if len(result.Content) == 0 {
		t.Fatal("expected content in tool response")
	}
	if !strings.Contains(result.Content[0].Text, "# Test Readme") {
		t.Errorf("expected tool response to contain file content, got %q", result.Content[0].Text)
	}
}

func TestMCPDeleteNotExposed(t *testing.T) {
	sid := mcpInit(t)
	resp := mcpCall(t, sid, 2, "tools/list", map[string]any{})

	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	json.Unmarshal(resp.Result, &result)

	for _, tool := range result.Tools {
		if strings.Contains(strings.ToLower(tool.Name), "delete") {
			t.Errorf("delete tool should not be exposed via MCP, found %q", tool.Name)
		}
	}
}

func TestMCPInvalidSession(t *testing.T) {
	req := jsonrpcRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list", Params: map[string]any{}}
	body, _ := json.Marshal(req)

	httpReq, _ := http.NewRequest("POST", baseURL+"/mcp", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Mcp-Session-Id", "bogus-session-id")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()

	// Server should reject with non-200 or JSON-RPC error
	if resp.StatusCode == 200 {
		var rpcResp jsonrpcResponse
		json.NewDecoder(resp.Body).Decode(&rpcResp)
		if rpcResp.Error == nil {
			t.Error("expected error for invalid session ID")
		}
	}
}

func TestIndex(t *testing.T) {
	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "trove") {
		t.Error("expected index page to contain 'trove'")
	}
}
