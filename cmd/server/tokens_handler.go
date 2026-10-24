package main

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gametimesf/open-trove/auth"
	"github.com/labstack/echo/v4"
)

type createTokenRequest struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days"`
}

type tokenResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type createTokenResponse struct {
	tokenResponse
	// Token is the plaintext credential. It is returned only once.
	Token string `json:"token"`
}

func toTokenResponse(t auth.Token) tokenResponse {
	return tokenResponse{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt}
}

// handleCreateToken mints a personal access token for the verified caller.
// Tokens can only be created from an identity-proxy session; an asserted
// email or another token is refused.
func (s *server) handleCreateToken(c echo.Context) error {
	var req createTokenRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if req.ExpiresInDays < 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "expires_in_days must be positive"})
	}
	ttl := time.Duration(req.ExpiresInDays) * 24 * time.Hour
	plaintext, token, err := s.tokens.Create(c.Request().Context(), verifiedPrincipal(c), req.Name, ttl)
	switch {
	case errors.Is(err, auth.ErrMintNotVerified):
		return c.JSON(http.StatusForbidden, map[string]string{"error": "creating tokens requires a verified sign-in"})
	case errors.Is(err, auth.ErrInvalidTokenInput):
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	case err != nil:
		log.Printf("ERROR creating token user_email=%q: %v", verifiedPrincipal(c).Email, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "creating token failed"})
	}
	log.Printf("INFO  token created user_email=%q token_id=%s expires_at=%s", token.OwnerEmail, token.DisplayID(), token.ExpiresAt.Format(time.RFC3339))
	return c.JSON(http.StatusCreated, createTokenResponse{tokenResponse: toTokenResponse(token), Token: plaintext})
}

// handleListTokens lists the verified caller's tokens.
func (s *server) handleListTokens(c echo.Context) error {
	tokens, err := s.tokens.List(c.Request().Context(), verifiedPrincipal(c))
	switch {
	case errors.Is(err, auth.ErrMissingCredential):
		return c.JSON(http.StatusForbidden, map[string]string{"error": "listing tokens requires a verified sign-in"})
	case err != nil:
		log.Printf("ERROR listing tokens user_email=%q: %v", verifiedPrincipal(c).Email, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "listing tokens failed"})
	}
	out := make([]tokenResponse, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toTokenResponse(t))
	}
	return c.JSON(http.StatusOK, map[string]any{"tokens": out})
}

// handleRevokeToken deletes one of the verified caller's tokens.
func (s *server) handleRevokeToken(c echo.Context) error {
	owner := verifiedPrincipal(c)
	err := s.tokens.Revoke(c.Request().Context(), owner, c.Param("id"))
	switch {
	case errors.Is(err, auth.ErrMissingCredential):
		return c.JSON(http.StatusForbidden, map[string]string{"error": "revoking tokens requires a verified sign-in"})
	case errors.Is(err, auth.ErrTokenNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": "token not found"})
	case err != nil:
		log.Printf("ERROR revoking token user_email=%q: %v", owner.Email, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "revoking token failed"})
	}
	log.Printf("INFO  token revoked user_email=%q token_id=%s", owner.Email, auth.Token{ID: c.Param("id")}.DisplayID())
	return c.NoContent(http.StatusNoContent)
}
