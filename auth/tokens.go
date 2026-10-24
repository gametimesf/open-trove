package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// SourceToken identifies principals authenticated by a Trove personal access token.
const SourceToken Source = "token"

// TokenPrefix marks Trove personal access tokens so they can be recognized
// (and secret-scanned) without a lookup.
const TokenPrefix = "trv_"

const (
	tokenSecretBytes = 32
	// DefaultTokenTTL applies when the caller does not choose an expiry.
	DefaultTokenTTL = 90 * 24 * time.Hour
	// MaxTokenTTL bounds how long a token may live.
	MaxTokenTTL = 365 * 24 * time.Hour
	// tokenTouchInterval throttles last-used writes.
	tokenTouchInterval = time.Hour
	maxTokenNameLength = 64
)

// Token errors.
var (
	ErrTokenNotFound  = errors.New("auth: token not found")
	ErrTokenExpired   = errors.New("auth: token expired")
	ErrTokenExists    = errors.New("auth: token already exists")
	ErrTokenMalformed = errors.New("auth: malformed token")
	// ErrSessionRequired means token management was attempted without a
	// verified identity-proxy session (an asserted email or a token).
	ErrSessionRequired   = errors.New("auth: managing tokens requires a verified identity-proxy session")
	ErrInvalidTokenInput = errors.New("auth: invalid token request")
)

// Token is the stored record for a personal access token. ID is the SHA-256 of
// the plaintext; the plaintext is never stored.
type Token struct {
	ID         string     `json:"id"`
	OwnerEmail string     `json:"owner_email"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// DisplayID is a short, non-secret identifier for logs and the UI.
func (t Token) DisplayID() string {
	if len(t.ID) < 12 {
		return t.ID
	}
	return t.ID[:12]
}

// TokenRepository is the persistence capability consumed by TokenService.
type TokenRepository interface {
	// CreateToken stores a new record and fails with ErrTokenExists if the ID is taken.
	CreateToken(ctx context.Context, token Token) error
	// GetToken returns ErrTokenNotFound for unknown or revoked tokens.
	GetToken(ctx context.Context, id string) (Token, error)
	ListTokens(ctx context.Context, ownerEmail string) ([]Token, error)
	// DeleteToken removes the owner's token or returns ErrTokenNotFound.
	DeleteToken(ctx context.Context, ownerEmail, id string) error
	// TouchToken records the last-used time.
	TouchToken(ctx context.Context, id string, usedAt time.Time) error
}

// TokenService issues, verifies, lists and revokes personal access tokens.
type TokenService struct {
	repo   TokenRepository
	now    func() time.Time
	random io.Reader
}

// NewTokenService returns a service backed by repo.
func NewTokenService(repo TokenRepository) *TokenService {
	return &TokenService{repo: repo, now: time.Now, random: rand.Reader}
}

// canManage reports whether p may create, list or revoke tokens. Only an
// identity-proxy session qualifies, so an asserted email can't manage tokens
// and a leaked token can't mint more or revoke its owner's others.
func canManage(p Principal) bool {
	return p.Verified && p.Source == SourceProxyJWT && p.Email != ""
}

// Create mints a token for owner and returns the plaintext, which must be
// shown once.
func (s *TokenService) Create(ctx context.Context, owner Principal, name string, ttl time.Duration) (string, Token, error) {
	if !canManage(owner) {
		return "", Token{}, ErrSessionRequired
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxTokenNameLength {
		return "", Token{}, fmt.Errorf("%w: name must be 1-%d characters", ErrInvalidTokenInput, maxTokenNameLength)
	}
	if ttl == 0 {
		ttl = DefaultTokenTTL
	}
	if ttl < 0 || ttl > MaxTokenTTL {
		return "", Token{}, fmt.Errorf("%w: expiry must be positive and at most 365 days", ErrInvalidTokenInput)
	}

	secret := make([]byte, tokenSecretBytes)
	if _, err := io.ReadFull(s.random, secret); err != nil {
		return "", Token{}, fmt.Errorf("generating token: %w", err)
	}
	plaintext := TokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	now := s.now().UTC()
	token := Token{
		ID:         hashToken(plaintext),
		OwnerEmail: owner.Email,
		Name:       name,
		CreatedAt:  now,
		ExpiresAt:  now.Add(ttl),
	}
	if err := s.repo.CreateToken(ctx, token); err != nil {
		return "", Token{}, fmt.Errorf("storing token: %w", err)
	}
	return plaintext, token, nil
}

// Verify returns the owner email for a valid, unexpired token.
func (s *TokenService) Verify(ctx context.Context, plaintext string) (string, error) {
	if !strings.HasPrefix(plaintext, TokenPrefix) {
		return "", ErrTokenMalformed
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(plaintext, TokenPrefix))
	if err != nil || len(raw) != tokenSecretBytes {
		return "", ErrTokenMalformed
	}
	token, err := s.repo.GetToken(ctx, hashToken(plaintext))
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return "", err
		}
		return "", fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
	}
	now := s.now().UTC()
	if !now.Before(token.ExpiresAt) {
		return "", ErrTokenExpired
	}
	if token.LastUsedAt == nil || now.Sub(*token.LastUsedAt) >= tokenTouchInterval {
		// Last-used is advisory; a failed write must not fail authentication.
		_ = s.repo.TouchToken(ctx, token.ID, now)
	}
	return token.OwnerEmail, nil
}

// List returns the owner's tokens.
func (s *TokenService) List(ctx context.Context, owner Principal) ([]Token, error) {
	if !canManage(owner) {
		return nil, ErrSessionRequired
	}
	tokens, err := s.repo.ListTokens(ctx, owner.Email)
	if err != nil {
		return nil, fmt.Errorf("listing tokens: %w", err)
	}
	return tokens, nil
}

// Revoke deletes one of the owner's tokens.
func (s *TokenService) Revoke(ctx context.Context, owner Principal, id string) error {
	if !canManage(owner) {
		return ErrSessionRequired
	}
	if err := s.repo.DeleteToken(ctx, owner.Email, id); err != nil {
		return fmt.Errorf("revoking token: %w", err)
	}
	return nil
}

func hashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
