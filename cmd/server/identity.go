package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gametimesf/open-trove/auth"
	"github.com/gametimesf/open-trove/internal/config"
	"github.com/labstack/echo/v4"
)

const (
	troveUserEmailHeader = "X-Trove-User-Email"
	troveUserEmailCookie = "trove_user_email"

	userEmailContextKey       = "trove_user_email"
	userEmailSourceContextKey = "trove_user_email_source"
	// verifiedPrincipalContextKey holds the auth.Principal proven by a verified source.
	verifiedPrincipalContextKey = "trove_verified_principal"
)

// requestIdentityMiddleware resolves user identity on every request. Asserted
// attribution comes from X-Trove-User-Email (API and agent clients) or the
// browser's cached email cookie. Verified sources, such as an identity-proxy
// JWT, are evaluated by the resolver according to its mode; see
// docs/authentication.md.
func requestIdentityMiddleware(resolver *auth.Resolver) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			asserted, assertedSource := resolveRequestUserEmail(req)
			write := isWriteMethod(req.Method)
			d := resolver.Resolve(req.Context(), auth.Credentials{
				BearerToken:   bearerTroveToken(req),
				ProxyJWT:      req.Header.Get(auth.CloudflareAccessHeader),
				AssertedEmail: asserted,
			}, write)

			source := assertedSource
			if d.Principal.Verified {
				source = string(d.Principal.Source)
			}
			log.Printf(
				"INFO  request identity method=%s path=%q user_email=%q source=%s verified_email=%q mismatch=%t",
				req.Method, req.URL.Path, d.Principal.Email, source, d.Verified.Email, d.Mismatch,
			)
			if d.VerifyErr != nil {
				log.Printf("WARN  identity verification failed method=%s path=%q mode=%s reason=%s",
					req.Method, req.URL.Path, resolver.Mode(), auth.Reason(d.VerifyErr))
			}
			if d.Deny != nil {
				log.Printf("WARN  auth denied method=%s path=%q reason=%s", req.Method, req.URL.Path, auth.Reason(d.Deny))
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			}

			if d.Verified.Verified {
				c.Set(verifiedPrincipalContextKey, d.Verified)
			}
			if d.Principal.Email != "" {
				c.Set(userEmailContextKey, d.Principal.Email)
				c.Set(userEmailSourceContextKey, source)
				// Give handlers and mounted integrations one canonical identity
				// header regardless of its source. A verified email replaces any
				// asserted value the client sent.
				req.Header.Set(troveUserEmailHeader, d.Principal.Email)
			}
			if write && d.Principal.Email == "" {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"error": fmt.Sprintf(
						"user email is required for write requests; send %s",
						troveUserEmailHeader,
					),
				})
			}
			return next(c)
		}
	}
}

// newIdentityResolver builds the request identity resolver, and the token
// service when tokens are enabled, from configuration. Off mode with no
// verified source reproduces asserted-only attribution.
func newIdentityResolver(ctx context.Context, cfg config.Auth, tokenRepo auth.TokenRepository) (*auth.Resolver, *auth.TokenService, error) {
	var sources auth.Sources
	if cfg.CloudflareAccess.Configured() {
		verifier, err := auth.NewCloudflareAccess(ctx, cfg.CloudflareAccess.TeamDomain, cfg.CloudflareAccess.Audience)
		if err != nil {
			return nil, nil, fmt.Errorf("configuring cloudflare access: %w", err)
		}
		sources.Proxy = verifier
	}
	var tokens *auth.TokenService
	if cfg.Tokens.Enabled {
		tokens = auth.NewTokenService(tokenRepo)
		sources.Tokens = tokens
	}
	resolver, err := auth.NewResolver(auth.Mode(cfg.Mode), auth.EnforceOn(cfg.EnforceOn), sources)
	if err != nil {
		return nil, nil, fmt.Errorf("configuring identity resolver: %w", err)
	}
	return resolver, tokens, nil
}

// bearerTroveToken returns a Trove personal access token from the
// Authorization header, or "" when the header is absent or carries another
// scheme or credential type.
func bearerTroveToken(r *http.Request) string {
	scheme, value, ok := strings.Cut(r.Header.Get(echo.HeaderAuthorization), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, auth.TokenPrefix) {
		return ""
	}
	return value
}

// verifiedPrincipal returns the principal proven by a verified source, even in
// shadow mode where handlers otherwise act on the asserted email.
func verifiedPrincipal(c echo.Context) auth.Principal {
	p, _ := c.Get(verifiedPrincipalContextKey).(auth.Principal)
	return p
}

func resolveRequestUserEmail(r *http.Request) (string, string) {
	if email := auth.NormalizeEmail(r.Header.Get(troveUserEmailHeader)); email != "" {
		return email, "header"
	}

	if cookie, err := r.Cookie(troveUserEmailCookie); err == nil {
		if email := auth.NormalizeEmail(cookie.Value); email != "" {
			return email, "cookie"
		}
	}

	return "", "missing"
}

func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func userEmail(c echo.Context) string {
	email, _ := c.Get(userEmailContextKey).(string)
	return email
}
