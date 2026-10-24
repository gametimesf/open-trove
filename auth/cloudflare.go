package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// CloudflareAccessHeader carries the signed Access JWT on every request that
// passed an Access application's policy. Plain-text identity headers such as
// Cf-Access-Authenticated-User-Email are deliberately not trusted.
const CloudflareAccessHeader = "Cf-Access-Jwt-Assertion"

// clockSkew tolerates small clock differences between Cloudflare and the origin.
const clockSkew = 30 * time.Second

const (
	// jwksFetchTimeout bounds each key fetch so a hung endpoint can't stall
	// startup (the first fetch runs before the server listens).
	jwksFetchTimeout = 5 * time.Second
	// jwksRefreshInterval also bounds how long a failed first fetch leaves
	// the verifier without keys.
	jwksRefreshInterval = 15 * time.Minute
)

// CloudflareAccess verifies Cloudflare Access application tokens using only
// the team's published public keys; it holds no secret.
type CloudflareAccess struct {
	keyfunc  jwt.Keyfunc
	issuer   string
	audience string
	now      func() time.Time
}

// NewCloudflareAccess verifies tokens issued by teamDomain (for example
// "example.cloudflareaccess.com") for the application audience tag. A failed
// or slow first key fetch (bounded by jwksFetchTimeout) does not prevent the
// service from starting; keys refresh periodically and on an unknown key ID.
func NewCloudflareAccess(ctx context.Context, teamDomain, audience string) (*CloudflareAccess, error) {
	if teamDomain == "" || audience == "" {
		return nil, fmt.Errorf("%w: cloudflare access requires team domain and audience", ErrInvalidConfig)
	}
	issuer := "https://" + teamDomain
	jwks, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{issuer + "/cdn-cgi/access/certs"}, keyfunc.Override{
		HTTPTimeout:     jwksFetchTimeout,
		RefreshInterval: jwksRefreshInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("creating cloudflare access key set: %w", err)
	}
	return newCloudflareAccess(jwks.Keyfunc, issuer, audience, time.Now), nil
}

func newCloudflareAccess(kf jwt.Keyfunc, issuer, audience string, now func() time.Time) *CloudflareAccess {
	return &CloudflareAccess{keyfunc: kf, issuer: issuer, audience: audience, now: now}
}

type accessClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// Verify checks signature, algorithm, issuer, audience and expiry, and
// returns the normalized email claim.
func (c *CloudflareAccess) Verify(_ context.Context, token string) (string, error) {
	claims := &accessClaims{}
	_, err := jwt.ParseWithClaims(token, claims, c.keyfunc,
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(c.issuer),
		jwt.WithAudience(c.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(clockSkew),
		jwt.WithTimeFunc(c.now),
	)
	if err != nil {
		return "", fmt.Errorf("verifying cloudflare access token: %w", err)
	}
	email := NormalizeEmail(claims.Email)
	if email == "" {
		return "", ErrMissingEmail
	}
	return email, nil
}

// Reason classifies a verification or denial error into a short, stable,
// non-sensitive label for logs.
func Reason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrMissingCredential):
		return "missing"
	case errors.Is(err, ErrSourceUnavailable):
		return "unavailable"
	case errors.Is(err, ErrMissingEmail):
		return "missing_email"
	case errors.Is(err, ErrTokenNotFound):
		return "unknown_token"
	case errors.Is(err, ErrTokenExpired):
		return "expired_token"
	case errors.Is(err, ErrTokenMalformed):
		return "malformed_token"
	case errors.Is(err, jwt.ErrTokenExpired):
		return "expired"
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return "audience"
	case errors.Is(err, jwt.ErrTokenInvalidIssuer):
		return "issuer"
	case errors.Is(err, jwt.ErrTokenRequiredClaimMissing):
		return "missing_claim"
	case errors.Is(err, jwt.ErrTokenMalformed):
		return "malformed"
	case errors.Is(err, jwt.ErrTokenSignatureInvalid), errors.Is(err, jwt.ErrTokenUnverifiable):
		return "invalid_signature"
	default:
		return "invalid"
	}
}
