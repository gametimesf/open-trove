package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://team.example.com"
	testAudience = "aud-tag"
)

var testNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

func newTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	return key
}

// tokenBuilder builds signed test tokens; each option mutates the defaults.
type tokenBuilder struct {
	method jwt.SigningMethod
	key    any
	claims jwt.MapClaims
}

func validToken(key *rsa.PrivateKey) *tokenBuilder {
	return &tokenBuilder{
		method: jwt.SigningMethodRS256,
		key:    key,
		claims: jwt.MapClaims{
			"iss":   testIssuer,
			"aud":   []string{testAudience},
			"exp":   testNow.Add(time.Hour).Unix(),
			"iat":   testNow.Unix(),
			"email": "Person@Example.com",
		},
	}
}

func (b *tokenBuilder) with(claim string, value any) *tokenBuilder {
	b.claims[claim] = value
	return b
}

func (b *tokenBuilder) without(claim string) *tokenBuilder {
	delete(b.claims, claim)
	return b
}

func (b *tokenBuilder) signedWith(method jwt.SigningMethod, key any) *tokenBuilder {
	b.method, b.key = method, key
	return b
}

func (b *tokenBuilder) sign(t *testing.T) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(b.method, b.claims).SignedString(b.key)
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return signed
}

func TestCloudflareAccessVerify(t *testing.T) {
	key := newTestKey(t)
	other := newTestKey(t)
	verifier := newCloudflareAccess(
		func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		testIssuer, testAudience, func() time.Time { return testNow },
	)

	tests := []struct {
		name       string
		token      string
		wantEmail  string
		wantReason string
	}{
		{name: "valid token normalizes email", token: validToken(key).sign(t), wantEmail: "person@example.com"},
		{name: "expiry within clock skew", token: validToken(key).with("exp", testNow.Add(-10*time.Second).Unix()).sign(t), wantEmail: "person@example.com"},
		{name: "signed by another key", token: validToken(other).sign(t), wantReason: "invalid_signature"},
		{name: "hmac algorithm rejected", token: validToken(key).signedWith(jwt.SigningMethodHS256, []byte("secret")).sign(t), wantReason: "invalid_signature"},
		{name: "none algorithm rejected", token: validToken(key).signedWith(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType).sign(t), wantReason: "invalid_signature"},
		{name: "expired", token: validToken(key).with("exp", testNow.Add(-time.Hour).Unix()).sign(t), wantReason: "expired"},
		{name: "missing expiry", token: validToken(key).without("exp").sign(t), wantReason: "missing_claim"},
		{name: "wrong audience", token: validToken(key).with("aud", []string{"other-app"}).sign(t), wantReason: "audience"},
		{name: "wrong issuer", token: validToken(key).with("iss", "https://evil.example.com").sign(t), wantReason: "issuer"},
		{name: "missing email", token: validToken(key).without("email").sign(t), wantReason: "missing_email"},
		{name: "invalid email", token: validToken(key).with("email", "not-an-email").sign(t), wantReason: "missing_email"},
		{name: "malformed", token: "not.a.jwt", wantReason: "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email, err := verifier.Verify(context.Background(), tt.token)
			if tt.wantReason != "" {
				if err == nil {
					t.Fatalf("Verify() succeeded with email %q, want reason %q", email, tt.wantReason)
				}
				if got := Reason(err); got != tt.wantReason {
					t.Fatalf("Reason() = %q, want %q (err: %v)", got, tt.wantReason, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Verify() error: %v", err)
			}
			if email != tt.wantEmail {
				t.Fatalf("Verify() email = %q, want %q", email, tt.wantEmail)
			}
		})
	}
}

func TestNewCloudflareAccessRequiresConfig(t *testing.T) {
	tests := []struct {
		name, team, aud string
	}{
		{"missing team", "", "aud"},
		{"missing audience", "team.example.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCloudflareAccess(context.Background(), tt.team, tt.aud)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestReason(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{ErrMissingCredential, "missing"},
		{errors.New("something else"), "invalid"},
	}
	for _, tt := range tests {
		if got := Reason(tt.err); got != tt.want {
			t.Errorf("Reason(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}
