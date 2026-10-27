package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/gametimesf/open-trove/auth"
	"github.com/gametimesf/open-trove/storage"
	"github.com/gametimesf/open-trove/storage/fake"
)

// newMCPTestEcho serves the API and /mcp with the given identity mode, a fake
// Access verifier and personal access tokens.
func newMCPTestEcho(t *testing.T, mode auth.Mode) (*echo.Echo, *fake.Store) {
	t.Helper()
	srv, store := newTestServer()
	srv.tokens = auth.NewTokenService(store)
	resolver, err := auth.NewResolver(mode, auth.EnforceWrites, auth.Sources{
		Proxy:  fakeProxy{token: tokenTestJWT, email: tokenTestEmail},
		Tokens: srv.tokens,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	srv.identity = resolver
	e := echo.New()
	registerRoutes(e, srv)
	if err := mountMCP(e); err != nil {
		t.Fatalf("mountMCP: %v", err)
	}
	if err := store.Put(t.Context(), "doc", strings.NewReader("<p>doc</p>"), storage.PutOptions{ContentType: "text/html", Filename: "doc.html"}); err != nil {
		t.Fatal(err)
	}
	return e, store
}

// mcpHeaders are the credentials on the outer /mcp request.
type mcpHeaders struct{ bearer, jwt, asserted string }

func mcpPost(t *testing.T, e *echo.Echo, h mcpHeaders, sid string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	if h.bearer != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+h.bearer)
	}
	if h.jwt != "" {
		req.Header.Set(auth.CloudflareAccessHeader, h.jwt)
	}
	if h.asserted != "" {
		req.Header.Set(troveUserEmailHeader, h.asserted)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// mcpCommentAuthor creates a comment through the MCP tool and returns the
// author Trove recorded, or "" with the HTTP status when /mcp refused the call.
func mcpCommentAuthor(t *testing.T, e *echo.Echo, store *fake.Store, h mcpHeaders, args map[string]any) (string, int) {
	t.Helper()
	initRec := mcpPost(t, e, h, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}},
	})
	if initRec.Code != http.StatusOK {
		return "", initRec.Code
	}
	sid := initRec.Header().Get("Mcp-Session-Id")
	call := mcpPost(t, e, h, sid, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "POST_api_artifacts_slug_comments", "arguments": args},
	})
	if call.Code != http.StatusOK {
		return "", call.Code
	}
	stored, err := store.ListComments(t.Context(), "doc")
	if err != nil || len(stored) != 1 {
		t.Fatalf("comments = %+v, %v; MCP response: %s", stored, err, call.Body.String())
	}
	return stored[0].AuthorEmail, call.Code
}

func TestMCPToolCallsUseForwardedIdentity(t *testing.T) {
	// echo-mcp sends arguments that aren't path, query or header parameters as
	// the JSON request body.
	commentArgs := func(asserted string) map[string]any {
		return map[string]any{"slug": "doc", "X-Trove-User-Email": asserted, "body": "from an agent", "anchor": map[string]any{"type": "file"}}
	}
	tests := []struct {
		name       string
		mode       auth.Mode
		headers    func(token string) mcpHeaders
		args       map[string]any
		wantAuthor string
		wantStatus int
	}{
		{
			name:       "bearer token on /mcp wins over the agent's email",
			mode:       auth.ModeEnforce,
			headers:    func(token string) mcpHeaders { return mcpHeaders{bearer: token} },
			args:       commentArgs("someone-else@example.com"),
			wantAuthor: tokenTestEmail, wantStatus: http.StatusOK,
		},
		{
			name:       "Access JWT on /mcp wins over the agent's email",
			mode:       auth.ModeEnforce,
			headers:    func(string) mcpHeaders { return mcpHeaders{jwt: tokenTestJWT} },
			args:       commentArgs("someone-else@example.com"),
			wantAuthor: tokenTestEmail, wantStatus: http.StatusOK,
		},
		{
			name:       "enforce refuses /mcp without credentials",
			mode:       auth.ModeEnforce,
			headers:    func(string) mcpHeaders { return mcpHeaders{asserted: "agent@example.com"} },
			args:       commentArgs("agent@example.com"),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "shadow keeps the asserted email when no credentials are sent",
			mode:       auth.ModeShadow,
			headers:    func(string) mcpHeaders { return mcpHeaders{asserted: "agent@example.com"} },
			args:       commentArgs("agent@example.com"),
			wantAuthor: "agent@example.com", wantStatus: http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, store := newMCPTestEcho(t, tt.mode)
			created := doTokenRequest(t, e, tokenRequest{method: http.MethodPost, path: "/api/tokens", body: `{"name":"mcp","expires_in_days":30}`, jwt: tokenTestJWT})
			var mint createTokenResponse
			if err := json.Unmarshal(created.Body.Bytes(), &mint); err != nil || mint.Token == "" {
				t.Fatalf("minting token: %v: %s", err, created.Body.String())
			}
			author, status := mcpCommentAuthor(t, e, store, tt.headers(mint.Token), tt.args)
			if status != tt.wantStatus || author != tt.wantAuthor {
				t.Fatalf("author = %q status = %d; want %q and %d", author, status, tt.wantAuthor, tt.wantStatus)
			}
		})
	}
}
