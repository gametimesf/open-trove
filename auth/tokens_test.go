package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// memoryTokens is an in-package TokenRepository; storage/fake can't be used
// here without an import cycle.
type memoryTokens struct {
	tokens  map[string]Token
	touches int
}

func newMemoryTokens() *memoryTokens { return &memoryTokens{tokens: map[string]Token{}} }

func (m *memoryTokens) CreateToken(_ context.Context, t Token) error {
	if _, ok := m.tokens[t.ID]; ok {
		return ErrTokenExists
	}
	m.tokens[t.ID] = t
	return nil
}

func (m *memoryTokens) GetToken(_ context.Context, id string) (Token, error) {
	t, ok := m.tokens[id]
	if !ok {
		return Token{}, ErrTokenNotFound
	}
	return t, nil
}

func (m *memoryTokens) ListTokens(_ context.Context, owner string) ([]Token, error) {
	var out []Token
	for _, t := range m.tokens {
		if t.OwnerEmail == owner {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *memoryTokens) DeleteToken(_ context.Context, owner, id string) error {
	t, ok := m.tokens[id]
	if !ok || t.OwnerEmail != owner {
		return ErrTokenNotFound
	}
	delete(m.tokens, id)
	return nil
}

func (m *memoryTokens) TouchToken(_ context.Context, id string, at time.Time) error {
	t := m.tokens[id]
	t.LastUsedAt = &at
	m.tokens[id] = t
	m.touches++
	return nil
}

var (
	proxyUser    = Principal{Email: "user@example.com", Source: SourceProxyJWT, Verified: true}
	tokenUser    = Principal{Email: "user@example.com", Source: SourceToken, Verified: true}
	assertedUser = Principal{Email: "user@example.com", Source: SourceAsserted}
)

func newTestTokenService(now *time.Time) (*TokenService, *memoryTokens) {
	repo := newMemoryTokens()
	svc := NewTokenService(repo)
	svc.now = func() time.Time { return *now }
	svc.random = bytes.NewReader(bytes.Repeat([]byte{7}, 4*tokenSecretBytes))
	return svc, repo
}

func TestTokenServiceCreate(t *testing.T) {
	now := testNow
	tests := []struct {
		name       string
		owner      Principal
		tokenName  string
		ttl        time.Duration
		wantErr    error
		wantExpiry time.Time
	}{
		{name: "proxy session with default ttl", owner: proxyUser, tokenName: "laptop cli", wantExpiry: now.Add(DefaultTokenTTL)},
		{name: "explicit ttl", owner: proxyUser, tokenName: "ci", ttl: 7 * 24 * time.Hour, wantExpiry: now.Add(7 * 24 * time.Hour)},
		{name: "asserted email cannot mint", owner: assertedUser, tokenName: "x", wantErr: ErrMintNotVerified},
		{name: "token cannot mint another token", owner: tokenUser, tokenName: "x", wantErr: ErrMintNotVerified},
		{name: "blank name", owner: proxyUser, tokenName: "  ", wantErr: ErrInvalidTokenInput},
		{name: "name too long", owner: proxyUser, tokenName: strings.Repeat("n", 65), wantErr: ErrInvalidTokenInput},
		{name: "ttl beyond max", owner: proxyUser, tokenName: "x", ttl: MaxTokenTTL + time.Hour, wantErr: ErrInvalidTokenInput},
		{name: "negative ttl", owner: proxyUser, tokenName: "x", ttl: -time.Hour, wantErr: ErrInvalidTokenInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newTestTokenService(&now)
			plaintext, token, err := svc.Create(context.Background(), tt.owner, tt.tokenName, tt.ttl)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Create() error = %v, want %v", err, tt.wantErr)
				}
				if len(repo.tokens) != 0 {
					t.Fatal("failed Create stored a token")
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() error: %v", err)
			}
			if !strings.HasPrefix(plaintext, TokenPrefix) {
				t.Errorf("plaintext %q missing prefix", plaintext)
			}
			stored := repo.tokens[token.ID]
			if stored.ID != hashToken(plaintext) || stored.OwnerEmail != "user@example.com" {
				t.Errorf("stored token = %+v", stored)
			}
			if strings.Contains(stored.ID, strings.TrimPrefix(plaintext, TokenPrefix)) {
				t.Error("stored record contains the plaintext secret")
			}
			if !token.ExpiresAt.Equal(tt.wantExpiry) {
				t.Errorf("ExpiresAt = %v, want %v", token.ExpiresAt, tt.wantExpiry)
			}
		})
	}
}

func TestTokenServiceVerify(t *testing.T) {
	now := testNow
	svc, repo := newTestTokenService(&now)
	plaintext, token, err := svc.Create(context.Background(), proxyUser, "cli", 24*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	email, err := svc.Verify(context.Background(), plaintext)
	if err != nil || email != "user@example.com" {
		t.Fatalf("Verify() = %q, %v", email, err)
	}
	if repo.touches != 1 {
		t.Fatalf("first use touches = %d, want 1", repo.touches)
	}
	now = now.Add(10 * time.Minute)
	if _, err := svc.Verify(context.Background(), plaintext); err != nil {
		t.Fatalf("Verify() again: %v", err)
	}
	if repo.touches != 1 {
		t.Fatalf("touch within interval was not throttled: touches = %d", repo.touches)
	}

	tests := []struct {
		name    string
		token   string
		advance time.Duration
		wantErr error
	}{
		{name: "wrong prefix", token: "ghp_" + strings.TrimPrefix(plaintext, TokenPrefix), wantErr: ErrTokenMalformed},
		{name: "truncated", token: plaintext[:len(plaintext)-4], wantErr: ErrTokenMalformed},
		{name: "unknown token", token: TokenPrefix + strings.Repeat("A", 43), wantErr: ErrTokenNotFound},
		{name: "expired", token: plaintext, advance: 24 * time.Hour, wantErr: ErrTokenExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at := now.Add(tt.advance)
			svc.now = func() time.Time { return at }
			if _, err := svc.Verify(context.Background(), tt.token); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Verify() error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	svc.now = func() time.Time { return now }
	if err := svc.Revoke(context.Background(), proxyUser, token.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := svc.Verify(context.Background(), plaintext); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("Verify() after revoke error = %v, want ErrTokenNotFound", err)
	}
}

func TestTokenServiceListAndRevokeRequireVerified(t *testing.T) {
	now := testNow
	svc, _ := newTestTokenService(&now)
	if _, err := svc.List(context.Background(), assertedUser); !errors.Is(err, ErrMissingCredential) {
		t.Fatalf("List(asserted) error = %v", err)
	}
	if err := svc.Revoke(context.Background(), assertedUser, "id"); !errors.Is(err, ErrMissingCredential) {
		t.Fatalf("Revoke(asserted) error = %v", err)
	}
	if _, err := svc.List(context.Background(), tokenUser); err != nil {
		t.Fatalf("List(token principal) error = %v", err)
	}
}

func TestResolveBearerToken(t *testing.T) {
	now := testNow
	svc, _ := newTestTokenService(&now)
	plaintext, _, err := svc.Create(context.Background(), proxyUser, "cli", time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	proxy := stubVerifier{token: "good", email: "proxy@example.com"}
	r, err := NewResolver(ModeEnforce, EnforceWrites, Sources{Proxy: proxy, Tokens: svc})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	tests := []struct {
		name       string
		creds      Credentials
		wantEmail  string
		wantSource Source
		wantDeny   error
	}{
		{name: "valid token", creds: Credentials{BearerToken: plaintext}, wantEmail: "user@example.com", wantSource: SourceToken},
		{name: "token decides over proxy jwt", creds: Credentials{BearerToken: plaintext, ProxyJWT: "good"}, wantEmail: "user@example.com", wantSource: SourceToken},
		{name: "invalid token is not downgraded to proxy jwt", creds: Credentials{BearerToken: TokenPrefix + strings.Repeat("A", 43), ProxyJWT: "good"}, wantDeny: ErrInvalidCredential},
		{name: "proxy jwt without token", creds: Credentials{ProxyJWT: "good"}, wantEmail: "proxy@example.com", wantSource: SourceProxyJWT},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := r.Resolve(context.Background(), tt.creds, true)
			if tt.wantDeny != nil {
				if !errors.Is(d.Deny, tt.wantDeny) {
					t.Fatalf("Deny = %v, want %v", d.Deny, tt.wantDeny)
				}
				return
			}
			if d.Deny != nil {
				t.Fatalf("unexpected Deny: %v", d.Deny)
			}
			if d.Principal.Email != tt.wantEmail || d.Principal.Source != tt.wantSource {
				t.Fatalf("Principal = %+v, want %q via %q", d.Principal, tt.wantEmail, tt.wantSource)
			}
		})
	}
}
