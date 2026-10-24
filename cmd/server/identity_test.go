package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gametimesf/open-trove/auth"
	"github.com/labstack/echo/v4"
)

func TestResolveRequestUserEmailPrefersExplicitHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(troveUserEmailHeader, "Jeff.Aronhalt@example.com")
	req.AddCookie(&http.Cookie{
		Name:  troveUserEmailCookie,
		Value: "cached@example.com",
	})

	email, source := resolveRequestUserEmail(req)
	if email != "jeff.aronhalt@example.com" {
		t.Fatalf("expected normalized header email, got %q", email)
	}
	if source != "header" {
		t.Fatalf("expected header source, got %q", source)
	}
}

func TestResolveRequestUserEmailFallsBackToBrowserCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(troveUserEmailHeader, "not-an-email")
	req.AddCookie(&http.Cookie{
		Name:  troveUserEmailCookie,
		Value: "browser@example.com",
	})

	email, source := resolveRequestUserEmail(req)
	if email != "browser@example.com" {
		t.Fatalf("expected cookie email, got %q", email)
	}
	if source != "cookie" {
		t.Fatalf("expected cookie source, got %q", source)
	}
}

func TestResolveRequestUserEmailMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	email, source := resolveRequestUserEmail(req)
	if email != "" || source != "missing" {
		t.Fatalf("expected missing identity, got email=%q source=%q", email, source)
	}
}

func TestRequestIdentityMiddlewareAllowsReadWithoutIdentity(t *testing.T) {
	e := echo.New()
	e.Use(requestIdentityMiddleware(mustOffResolver()))
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRequestIdentityMiddlewareRequiresIdentityForWrites(t *testing.T) {
	e := echo.New()
	e.Use(requestIdentityMiddleware(mustOffResolver()))
	e.POST("/write", func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/write", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), troveUserEmailHeader) {
		t.Fatalf("expected response to name required header: %s", rec.Body.String())
	}
}

func TestRequestIdentityMiddlewareSetsCanonicalHeaderFromCookie(t *testing.T) {
	e := echo.New()
	e.Use(requestIdentityMiddleware(mustOffResolver()))
	e.POST("/write", func(c echo.Context) error {
		if got := c.Request().Header.Get(troveUserEmailHeader); got != "browser@example.com" {
			t.Fatalf("expected canonical identity header, got %q", got)
		}
		if got := userEmail(c); got != "browser@example.com" {
			t.Fatalf("expected context identity, got %q", got)
		}
		return c.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/write", nil)
	req.AddCookie(&http.Cookie{
		Name:  troveUserEmailCookie,
		Value: "browser@example.com",
	})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestNormalizeEmailRejectsDisplayNamesAndInvalidValues(t *testing.T) {
	for _, value := range []string{
		"",
		"Jeff <jeff@example.com>",
		"not-an-email",
		"jeff@gametime",
	} {
		if got := auth.NormalizeEmail(value); got != "" {
			t.Errorf("auth.NormalizeEmail(%q) = %q, want empty", value, got)
		}
	}
}

func TestUserIdentityJSIncludesPromptCacheAndHeader(t *testing.T) {
	for _, want := range []string{
		"What’s your email?",
		"window.localStorage",
		"trove_user_email",
		"X-Trove-User-Email",
		"window.fetch",
	} {
		if !strings.Contains(userIdentityJS, want) {
			t.Errorf("identity JavaScript missing %q", want)
		}
	}
}

func TestUserIdentityScriptRoute(t *testing.T) {
	srv, _ := newTestServer()
	e := newTestEcho(srv)

	req := httptest.NewRequest(http.MethodGet, "/identity.js", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(echo.HeaderContentType); !strings.Contains(got, "application/javascript") {
		t.Fatalf("expected JavaScript content type, got %q", got)
	}
}

// mustOffResolver returns the default asserted-only resolver.
func mustOffResolver() *auth.Resolver {
	r, err := auth.NewResolver(auth.ModeOff, auth.EnforceWrites, auth.Sources{})
	if err != nil {
		panic(err)
	}
	return r
}

// fakeProxy accepts exactly one token for one email.
type fakeProxy struct{ token, email string }

func (f fakeProxy) Verify(_ context.Context, token string) (string, error) {
	if token != f.token {
		return "", errors.New("bad token")
	}
	return f.email, nil
}

func TestRequestIdentityMiddlewareModes(t *testing.T) {
	proxy := fakeProxy{token: "good-jwt", email: "verified@example.com"}
	tests := []struct {
		name       string
		mode       auth.Mode
		method     string
		jwt        string
		asserted   string
		wantStatus int
		wantEmail  string
	}{
		{name: "shadow keeps asserted email on write", mode: auth.ModeShadow, method: http.MethodPost, jwt: "good-jwt", asserted: "asserted@example.com", wantStatus: http.StatusOK, wantEmail: "asserted@example.com"},
		{name: "shadow does not block invalid jwt", mode: auth.ModeShadow, method: http.MethodPost, jwt: "forged", asserted: "asserted@example.com", wantStatus: http.StatusOK, wantEmail: "asserted@example.com"},
		{name: "shadow uses verified email when none is asserted", mode: auth.ModeShadow, method: http.MethodPost, jwt: "good-jwt", wantStatus: http.StatusOK, wantEmail: "verified@example.com"},
		{name: "shadow keeps 400 for write without any identity", mode: auth.ModeShadow, method: http.MethodPost, wantStatus: http.StatusBadRequest},
		{name: "enforce replaces asserted with verified", mode: auth.ModeEnforce, method: http.MethodPost, jwt: "good-jwt", asserted: "someone-else@example.com", wantStatus: http.StatusOK, wantEmail: "verified@example.com"},
		{name: "enforce accepts verified write without asserted email", mode: auth.ModeEnforce, method: http.MethodPost, jwt: "good-jwt", wantStatus: http.StatusOK, wantEmail: "verified@example.com"},
		{name: "enforce rejects asserted-only write", mode: auth.ModeEnforce, method: http.MethodPost, asserted: "asserted@example.com", wantStatus: http.StatusUnauthorized},
		{name: "enforce rejects forged jwt on read", mode: auth.ModeEnforce, method: http.MethodGet, jwt: "forged", wantStatus: http.StatusUnauthorized},
		{name: "enforce allows anonymous read", mode: auth.ModeEnforce, method: http.MethodGet, wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver, err := auth.NewResolver(tt.mode, auth.EnforceWrites, auth.Sources{Proxy: proxy})
			if err != nil {
				t.Fatalf("NewResolver: %v", err)
			}
			e := echo.New()
			e.Use(requestIdentityMiddleware(resolver))
			var gotEmail, gotHeader string
			handler := func(c echo.Context) error {
				gotEmail = userEmail(c)
				gotHeader = c.Request().Header.Get(troveUserEmailHeader)
				return c.String(http.StatusOK, "ok")
			}
			e.GET("/", handler)
			e.POST("/", handler)

			req := httptest.NewRequest(tt.method, "/", nil)
			if tt.jwt != "" {
				req.Header.Set(auth.CloudflareAccessHeader, tt.jwt)
			}
			if tt.asserted != "" {
				req.Header.Set(troveUserEmailHeader, tt.asserted)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if gotEmail != tt.wantEmail {
				t.Errorf("handler email = %q, want %q", gotEmail, tt.wantEmail)
			}
			if gotHeader != tt.wantEmail {
				t.Errorf("canonical header = %q, want %q", gotHeader, tt.wantEmail)
			}
		})
	}
}

func TestUserIdentityJSVerifiedEmail(t *testing.T) {
	tests := []struct {
		name         string
		jwt          string
		wantInjected string
	}{
		{name: "verified browser gets its email", jwt: "good-jwt", wantInjected: `window.__troveVerifiedEmail = "verified@example.com";`},
		{name: "unverified browser keeps the prompt", wantInjected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver, err := auth.NewResolver(auth.ModeShadow, auth.EnforceWrites, auth.Sources{Proxy: fakeProxy{token: "good-jwt", email: "verified@example.com"}})
			if err != nil {
				t.Fatalf("NewResolver: %v", err)
			}
			srv, _ := newTestServer()
			srv.identity = resolver
			e := echo.New()
			registerRoutes(e, srv)

			req := httptest.NewRequest(http.MethodGet, "/identity.js", nil)
			if tt.jwt != "" {
				req.Header.Set(auth.CloudflareAccessHeader, tt.jwt)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
				t.Errorf("Cache-Control = %q, want private, no-store", cc)
			}
			body := rec.Body.String()
			if tt.wantInjected != "" && !strings.HasPrefix(body, tt.wantInjected) {
				t.Errorf("script does not start with %q", tt.wantInjected)
			}
			if tt.wantInjected == "" && strings.Contains(body, "window.__troveVerifiedEmail =") {
				t.Error("unverified request received an injected email")
			}
		})
	}
}
