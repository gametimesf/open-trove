package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gametimesf/open-trove/auth"
	"github.com/gametimesf/open-trove/storage"
	"github.com/labstack/echo/v4"
)

const (
	tokenTestJWT   = "good-jwt"
	tokenTestEmail = "verified@example.com"
)

// newTokenTestEcho wires a server in the given mode with Cloudflare Access
// (faked) and personal access tokens enabled.
func newTokenTestEcho(t *testing.T, mode auth.Mode) *echo.Echo {
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
	// A minimal write route to observe which identity a token resolves to.
	e.POST("/_test/whoami", func(c echo.Context) error {
		return c.String(http.StatusOK, userEmail(c))
	})
	return e
}

type tokenRequest struct {
	method, path, body string
	jwt, bearer        string
	asserted           string
}

func doTokenRequest(t *testing.T, e *echo.Echo, r tokenRequest) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if r.jwt != "" {
		req.Header.Set(auth.CloudflareAccessHeader, r.jwt)
	}
	if r.bearer != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+r.bearer)
	}
	if r.asserted != "" {
		req.Header.Set(troveUserEmailHeader, r.asserted)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestTokenLifecycleEnforce(t *testing.T) {
	e := newTokenTestEcho(t, auth.ModeEnforce)

	created := doTokenRequest(t, e, tokenRequest{method: http.MethodPost, path: "/api/tokens", body: `{"name":"laptop","expires_in_days":30}`, jwt: tokenTestJWT})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", created.Code, created.Body.String())
	}
	var mint createTokenResponse
	if err := json.Unmarshal(created.Body.Bytes(), &mint); err != nil {
		t.Fatalf("decoding create: %v", err)
	}
	if !strings.HasPrefix(mint.Token, auth.TokenPrefix) || mint.Name != "laptop" {
		t.Fatalf("create response = %+v", mint)
	}

	who := doTokenRequest(t, e, tokenRequest{method: http.MethodPost, path: "/_test/whoami", bearer: mint.Token, asserted: "someone-else@example.com"})
	if who.Code != http.StatusOK || who.Body.String() != tokenTestEmail {
		t.Fatalf("token write as %q (status %d), want %q", who.Body.String(), who.Code, tokenTestEmail)
	}

	list := doTokenRequest(t, e, tokenRequest{method: http.MethodGet, path: "/api/tokens", bearer: mint.Token})
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), mint.ID) {
		t.Fatalf("list status = %d: %s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), mint.Token) {
		t.Fatal("list response leaked the plaintext token")
	}

	minted := doTokenRequest(t, e, tokenRequest{method: http.MethodPost, path: "/api/tokens", body: `{"name":"chained"}`, bearer: mint.Token})
	if minted.Code != http.StatusForbidden {
		t.Fatalf("token minting another token status = %d, want 403", minted.Code)
	}

	revoked := doTokenRequest(t, e, tokenRequest{method: http.MethodDelete, path: "/api/tokens/" + mint.ID, jwt: tokenTestJWT})
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d: %s", revoked.Code, revoked.Body.String())
	}
	after := doTokenRequest(t, e, tokenRequest{method: http.MethodPost, path: "/_test/whoami", bearer: mint.Token})
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d, want 401", after.Code)
	}
}

func TestTokenEndpointsRequireVerifiedPrincipal(t *testing.T) {
	tests := []struct {
		name       string
		mode       auth.Mode
		req        tokenRequest
		wantStatus int
	}{
		{name: "shadow asserted email cannot mint", mode: auth.ModeShadow, req: tokenRequest{method: http.MethodPost, path: "/api/tokens", body: `{"name":"x"}`, asserted: tokenTestEmail}, wantStatus: http.StatusForbidden},
		{name: "shadow proxy session can mint", mode: auth.ModeShadow, req: tokenRequest{method: http.MethodPost, path: "/api/tokens", body: `{"name":"x"}`, jwt: tokenTestJWT, asserted: tokenTestEmail}, wantStatus: http.StatusCreated},
		{name: "shadow asserted email cannot list", mode: auth.ModeShadow, req: tokenRequest{method: http.MethodGet, path: "/api/tokens", asserted: tokenTestEmail}, wantStatus: http.StatusForbidden},
		{name: "enforce rejects unauthenticated mint", mode: auth.ModeEnforce, req: tokenRequest{method: http.MethodPost, path: "/api/tokens", body: `{"name":"x"}`, asserted: tokenTestEmail}, wantStatus: http.StatusUnauthorized},
		{name: "invalid name", mode: auth.ModeEnforce, req: tokenRequest{method: http.MethodPost, path: "/api/tokens", body: `{"name":""}`, jwt: tokenTestJWT}, wantStatus: http.StatusBadRequest},
		{name: "revoke unknown token", mode: auth.ModeEnforce, req: tokenRequest{method: http.MethodDelete, path: "/api/tokens/" + strings.Repeat("a", 64), jwt: tokenTestJWT}, wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTokenTestEcho(t, tt.mode)
			rec := doTokenRequest(t, e, tt.req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestTokenRoutesAbsentWhenDisabled(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)
	req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("token routes registered while disabled: status %d", rec.Code)
	}
}

func TestBearerTroveToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"Bearer trv_abc", "trv_abc"},
		{"bearer trv_abc", "trv_abc"},
		{"Bearer ghp_other", ""},
		{"Basic trv_abc", ""},
		{"", ""},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if tt.header != "" {
			req.Header.Set(echo.HeaderAuthorization, tt.header)
		}
		if got := bearerTroveToken(req); got != tt.want {
			t.Errorf("bearerTroveToken(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestMyTroveTokenSection(t *testing.T) {
	tests := []struct {
		name        string
		view        myTroveView
		contains    []string
		notContains []string
	}{
		{name: "disabled hides section", view: myTroveView{UserManifest: &storage.UserManifest{}}, notContains: []string{"API tokens"}},
		{name: "unverified explains sign-in", view: myTroveView{UserManifest: &storage.UserManifest{}, TokensEnabled: true}, contains: []string{"API tokens", "Sign in through"}, notContains: []string{"token-form"}},
		{name: "verified shows form and escapes email", view: myTroveView{UserManifest: &storage.UserManifest{}, TokensEnabled: true, VerifiedEmail: "a<b>@example.com"}, contains: []string{"token-form", "a&lt;b&gt;@example.com"}, notContains: []string{"a<b>@example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var page strings.Builder
			if err := myTroveTemplate.Execute(&page, tt.view); err != nil {
				t.Fatalf("rendering: %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(page.String(), want) {
					t.Errorf("page missing %q", want)
				}
			}
			for _, unwanted := range tt.notContains {
				if strings.Contains(page.String(), unwanted) {
					t.Errorf("page unexpectedly contains %q", unwanted)
				}
			}
		})
	}
}
