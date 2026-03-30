// Command mcp-client exercises the trove API end-to-end via both HTTP and MCP.
//
// Usage:
//
//	mcp-client [-url <base-url>] <command>
//
// Commands:
//
//	run           Full end-to-end flow (HTTP + MCP)
//	http          HTTP-only flow
//	mcp           MCP-only flow
//	tools         List MCP tools
//	call <tool>   Call an MCP tool (pass args as key=value pairs)
//
// The "run" command exercises: llms.txt, agent.json, swagger, upload, view,
// raw download, /mine activity, and the same via MCP tools.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
)

const defaultURL = "http://localhost:8080"

// ─── JSON-RPC types ──────────────────────────────────────────────

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

// ─── MCP client ──────────────────────────────────────────────────

type mcpClient struct {
	baseURL   string
	sessionID string
	nextID    int
}

func newMCPClient(baseURL string) *mcpClient {
	return &mcpClient{baseURL: baseURL, nextID: 1}
}

func (c *mcpClient) call(method string, params any) (*jsonrpcResponse, error) {
	req := jsonrpcRequest{JSONRPC: "2.0", ID: c.nextID, Method: method, Params: params}
	c.nextID++

	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", c.baseURL+"/mcp", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, respBody)
	}

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	var rpcResp jsonrpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return &rpcResp, nil
}

func (c *mcpClient) initialize() error {
	_, err := c.call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":   map[string]any{},
		"clientInfo":     map[string]any{"name": "trove-mcp-client", "version": "1.0"},
	})
	return err
}

func (c *mcpClient) listTools() ([]struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}, error) {
	resp, err := c.call("tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshaling result: %w", err)
	}
	return result.Tools, nil
}

func (c *mcpClient) callTool(name string, args map[string]any) (string, error) {
	resp, err := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("unmarshaling result: %w", err)
	}
	if len(result.Content) == 0 {
		return "", fmt.Errorf("no content in response")
	}
	return result.Content[0].Text, nil
}

// ─── HTTP helpers ────────────────────────────────────────────────

func httpGet(client *http.Client, url string) (int, string, http.Header, error) {
	resp, err := client.Get(url)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header, nil
}

func httpUpload(client *http.Client, baseURL, filename string, content []byte, slug string, overwrite bool) (map[string]string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", filename)
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
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding upload response: %w", err)
	}
	return result, nil
}

// ─── Flow runners ────────────────────────────────────────────────

type step struct {
	name string
	fn   func() error
}

func runSteps(label string, steps []step) bool {
	fmt.Printf("\n━━━ %s ━━━\n", label)
	passed := 0
	for _, s := range steps {
		if err := s.fn(); err != nil {
			fmt.Printf("  ✗ %s: %v\n", s.name, err)
		} else {
			fmt.Printf("  ✓ %s\n", s.name)
			passed++
		}
	}
	fmt.Printf("  %d/%d passed\n", passed, len(steps))
	return passed == len(steps)
}

func runHTTP(baseURL string) bool {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	slug := "e2e-http-test"
	content := []byte("# E2E HTTP Test\n\nUploaded by mcp-client.\n")

	return runSteps("HTTP Flow", []step{
		{"GET /llms.txt", func() error {
			code, body, _, err := httpGet(client, baseURL+"/llms.txt")
			if err != nil {
				return err
			}
			if code != 200 {
				return fmt.Errorf("status %d", code)
			}
			if !strings.Contains(body, "# Trove") {
				return fmt.Errorf("missing expected content")
			}
			return nil
		}},
		{"GET /.well-known/agent.json", func() error {
			code, body, _, err := httpGet(client, baseURL+"/.well-known/agent.json")
			if err != nil {
				return err
			}
			if code != 200 {
				return fmt.Errorf("status %d", code)
			}
			if !strings.Contains(body, `"name":"trove"`) {
				return fmt.Errorf("missing trove name")
			}
			return nil
		}},
		{"GET /openapi.json", func() error {
			code, _, hdr, err := httpGet(client, baseURL+"/openapi.json")
			if err != nil {
				return err
			}
			if code != 200 {
				return fmt.Errorf("status %d", code)
			}
			if !strings.Contains(hdr.Get("Content-Type"), "json") {
				return fmt.Errorf("unexpected content-type: %s", hdr.Get("Content-Type"))
			}
			return nil
		}},
		{"POST /upload", func() error {
			result, err := httpUpload(client, baseURL, "test.md", content, slug, true)
			if err != nil {
				return err
			}
			if result["slug"] != slug {
				return fmt.Errorf("expected slug %q, got %q", slug, result["slug"])
			}
			return nil
		}},
		{"GET /{slug} (view)", func() error {
			code, body, _, err := httpGet(client, baseURL+"/"+slug)
			if err != nil {
				return err
			}
			if code != 200 {
				return fmt.Errorf("status %d", code)
			}
			if !strings.Contains(body, slug) {
				return fmt.Errorf("view page missing slug reference")
			}
			return nil
		}},
		{"GET /{slug}/raw", func() error {
			code, body, hdr, err := httpGet(client, baseURL+"/"+slug+"/raw")
			if err != nil {
				return err
			}
			if code != 200 {
				return fmt.Errorf("status %d", code)
			}
			if body != string(content) {
				return fmt.Errorf("raw content mismatch")
			}
			if !strings.Contains(hdr.Get("Content-Type"), "markdown") {
				return fmt.Errorf("unexpected content-type: %s", hdr.Get("Content-Type"))
			}
			return nil
		}},
		{"GET /mine (shows upload)", func() error {
			code, body, _, err := httpGet(client, baseURL+"/mine")
			if err != nil {
				return err
			}
			if code != 200 {
				return fmt.Errorf("status %d", code)
			}
			if !strings.Contains(body, slug) {
				return fmt.Errorf("/mine does not contain uploaded slug")
			}
			return nil
		}},
		{"DELETE /{slug}", func() error {
			req, _ := http.NewRequest("DELETE", baseURL+"/delete/"+slug, nil)
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			resp.Body.Close()
			if resp.StatusCode != 204 {
				return fmt.Errorf("expected 204, got %d", resp.StatusCode)
			}
			return nil
		}},
		{"GET /{slug}/raw (after delete)", func() error {
			code, _, _, err := httpGet(client, baseURL+"/"+slug+"/raw")
			if err != nil {
				return err
			}
			if code != 404 {
				return fmt.Errorf("expected 404 after delete, got %d", code)
			}
			return nil
		}},
		{"GET /healthz", func() error {
			code, _, _, err := httpGet(client, baseURL+"/healthz")
			if err != nil {
				return err
			}
			if code != 200 {
				return fmt.Errorf("status %d", code)
			}
			return nil
		}},
	})
}

func runMCP(baseURL string) bool {
	c := newMCPClient(baseURL)
	slug := "e2e-mcp-test"

	return runSteps("MCP Flow", []step{
		{"initialize", func() error {
			return c.initialize()
		}},
		{"tools/list", func() error {
			tools, err := c.listTools()
			if err != nil {
				return err
			}
			if len(tools) == 0 {
				return fmt.Errorf("no tools returned")
			}
			names := make(map[string]bool, len(tools))
			for _, t := range tools {
				names[t.Name] = true
			}
			for _, want := range []string{"POST_upload", "GET_slug", "GET_slug_raw", "GET_llmstxt", "GET_mine"} {
				if !names[want] {
					return fmt.Errorf("missing tool %q", want)
				}
			}
			return nil
		}},
		{"call GET_llmstxt", func() error {
			text, err := c.callTool("GET_llmstxt", map[string]any{})
			if err != nil {
				return err
			}
			if !strings.Contains(text, "# Trove") {
				return fmt.Errorf("missing expected content")
			}
			return nil
		}},
		{"call GET_well-known_agentjson", func() error {
			text, err := c.callTool("GET_well-known_agentjson", map[string]any{})
			if err != nil {
				return err
			}
			if !strings.Contains(text, "trove") {
				return fmt.Errorf("missing trove in agent.json")
			}
			return nil
		}},
		{"call GET_openapijson", func() error {
			text, err := c.callTool("GET_openapijson", map[string]any{})
			if err != nil {
				return err
			}
			if !strings.Contains(text, "openapi") && !strings.Contains(text, "swagger") {
				return fmt.Errorf("missing openapi/swagger content")
			}
			return nil
		}},
		{"upload via HTTP (MCP upload requires multipart)", func() error {
			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar}
			_, err := httpUpload(client, baseURL, "test.md", []byte("# E2E MCP Test\n"), slug, true)
			return err
		}},
		{"call GET_slug_raw", func() error {
			text, err := c.callTool("GET_slug_raw", map[string]any{"slug": slug})
			if err != nil {
				return err
			}
			if !strings.Contains(text, "E2E MCP Test") {
				return fmt.Errorf("missing uploaded content in raw response")
			}
			return nil
		}},
		{"call GET_slug (view)", func() error {
			text, err := c.callTool("GET_slug", map[string]any{"slug": slug})
			if err != nil {
				return err
			}
			if !strings.Contains(text, slug) {
				return fmt.Errorf("view response missing slug reference")
			}
			return nil
		}},
		{"call GET_mine", func() error {
			_, err := c.callTool("GET_mine", map[string]any{})
			return err
		}},
		{"delete not exposed via MCP", func() error {
			tools, err := c.listTools()
			if err != nil {
				return err
			}
			for _, t := range tools {
				if strings.Contains(strings.ToLower(t.Name), "delete") {
					return fmt.Errorf("delete tool should not be exposed, found %q", t.Name)
				}
			}
			return nil
		}},
		{"DELETE /{slug} via HTTP", func() error {
			req, _ := http.NewRequest("DELETE", baseURL+"/delete/"+slug, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			resp.Body.Close()
			if resp.StatusCode != 204 {
				return fmt.Errorf("expected 204, got %d", resp.StatusCode)
			}
			return nil
		}},
		{"confirm deleted via MCP", func() error {
			_, err := c.callTool("GET_slug_raw", map[string]any{"slug": slug})
			if err == nil {
				return fmt.Errorf("expected error for deleted slug")
			}
			return nil
		}},
	})
}

// ─── CLI ─────────────────────────────────────────────────────────

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: mcp-client [-url <base-url>] <command> [args...]

Commands:
  run                   Full end-to-end flow (HTTP + MCP)
  http                  HTTP-only flow
  mcp                   MCP-only flow
  tools                 List MCP tools
  call <tool> [k=v...]  Call an MCP tool

Default URL: %s
`, defaultURL)
	os.Exit(1)
}

func main() {
	url := defaultURL
	args := os.Args[1:]

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-url":
			if len(args) < 2 {
				usage()
			}
			url = args[1]
			args = args[2:]
		default:
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[0])
			usage()
		}
	}

	if len(args) == 0 {
		usage()
	}

	switch args[0] {
	case "run":
		httpOK := runHTTP(url)
		mcpOK := runMCP(url)
		fmt.Println()
		if httpOK && mcpOK {
			fmt.Println("All flows passed.")
		} else {
			fmt.Println("Some steps failed.")
			os.Exit(1)
		}

	case "http":
		if !runHTTP(url) {
			os.Exit(1)
		}

	case "mcp":
		if !runMCP(url) {
			os.Exit(1)
		}

	case "tools":
		c := newMCPClient(url)
		if err := c.initialize(); err != nil {
			fmt.Fprintf(os.Stderr, "initialize failed: %v\n", err)
			os.Exit(1)
		}
		tools, err := c.listTools()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tools/list failed: %v\n", err)
			os.Exit(1)
		}
		for _, t := range tools {
			fmt.Printf("  %-25s %s\n", t.Name, t.Description)
		}

	case "call":
		if len(args) < 2 {
			usage()
		}
		c := newMCPClient(url)
		if err := c.initialize(); err != nil {
			fmt.Fprintf(os.Stderr, "initialize failed: %v\n", err)
			os.Exit(1)
		}
		toolArgs := make(map[string]any)
		for _, arg := range args[2:] {
			k, v, ok := strings.Cut(arg, "=")
			if !ok {
				fmt.Fprintf(os.Stderr, "invalid argument %q (expected key=value)\n", arg)
				os.Exit(1)
			}
			toolArgs[k] = v
		}
		text, err := c.callTool(args[1], toolArgs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "call failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(text)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		usage()
	}
}
