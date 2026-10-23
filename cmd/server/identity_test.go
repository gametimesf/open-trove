package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	e.Use(requestIdentityMiddleware())
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
	e.Use(requestIdentityMiddleware())
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
	e.Use(requestIdentityMiddleware())
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
		if got := normalizeEmail(value); got != "" {
			t.Errorf("normalizeEmail(%q) = %q, want empty", value, got)
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
