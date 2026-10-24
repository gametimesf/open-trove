// Package auth decides who is making a request from the credentials it
// presents. It is independent of HTTP: transport adapters extract Credentials
// and act on the returned Decision. See docs/authentication.md.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

// Source identifies where a principal's email came from.
type Source string

// Principal sources.
const (
	SourceProxyJWT Source = "proxy_jwt"
	SourceAsserted Source = "asserted"
)

// Principal is the identity a request acts as.
type Principal struct {
	Email    string
	Source   Source
	Verified bool
}

// Mode controls how verified sources affect request handling.
type Mode string

// Modes.
const (
	// ModeOff uses the asserted email only. It is the default.
	ModeOff Mode = "off"
	// ModeShadow verifies and reports credentials but keeps asserted behavior:
	// handlers act on the asserted email when one is sent, and on the verified
	// email only when none is.
	ModeShadow Mode = "shadow"
	// ModeEnforce acts on the verified principal and rejects missing or
	// invalid credentials for requests in scope.
	ModeEnforce Mode = "enforce"
)

// EnforceOn selects which requests require a verified principal in ModeEnforce.
type EnforceOn string

// Enforcement scopes.
const (
	EnforceWrites EnforceOn = "writes"
	EnforceAll    EnforceOn = "all"
)

// Errors returned in Decision.Deny and Decision.VerifyErr.
var (
	ErrMissingCredential = errors.New("auth: verified credential required")
	ErrInvalidCredential = errors.New("auth: invalid credential")
	ErrMissingEmail      = errors.New("auth: credential has no email")
	ErrInvalidConfig     = errors.New("auth: invalid configuration")
)

// Credentials are the raw identity inputs on a request. Empty fields were not
// presented. AssertedEmail must already be normalized (see NormalizeEmail).
type Credentials struct {
	BearerToken   string // value after "Bearer ", only when it has TokenPrefix
	ProxyJWT      string
	AssertedEmail string
}

// ProxyVerifier verifies a signed assertion from an identity-aware proxy and
// returns the normalized email it vouches for.
type ProxyVerifier interface {
	Verify(ctx context.Context, token string) (string, error)
}

// Decision is the outcome of resolving one request.
type Decision struct {
	// Principal is the identity handlers act as. Email is empty when anonymous.
	Principal Principal
	// Verified is the principal proven by a verified source, if any. In shadow
	// mode it is reported but not acted on.
	Verified Principal
	// Mismatch reports a verified email that differs from the asserted one.
	Mismatch bool
	// VerifyErr is set when a presented credential failed verification.
	VerifyErr error
	// Deny is set when the request must be rejected as unauthenticated.
	Deny error
}

// TokenVerifier verifies a Trove personal access token and returns its owner.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (string, error)
}

// Sources are the verified credential sources available to a Resolver. Nil
// fields are not configured.
type Sources struct {
	Proxy  ProxyVerifier
	Tokens TokenVerifier
}

// Resolver applies a mode to the credentials on each request.
type Resolver struct {
	mode      Mode
	enforceOn EnforceOn
	sources   Sources
}

// NewResolver validates the combination of mode, scope and sources. Shadow
// and enforce modes require a verified source; there is nothing to verify
// without one.
func NewResolver(mode Mode, enforceOn EnforceOn, sources Sources) (*Resolver, error) {
	switch mode {
	case ModeOff:
	case ModeShadow, ModeEnforce:
		if sources.Proxy == nil && sources.Tokens == nil {
			return nil, fmt.Errorf("%w: mode %q requires a verified credential source", ErrInvalidConfig, mode)
		}
	default:
		return nil, fmt.Errorf("%w: unknown mode %q", ErrInvalidConfig, mode)
	}
	switch enforceOn {
	case EnforceWrites, EnforceAll:
	default:
		return nil, fmt.Errorf("%w: unknown enforcement scope %q", ErrInvalidConfig, enforceOn)
	}
	return &Resolver{mode: mode, enforceOn: enforceOn, sources: sources}, nil
}

// Mode reports the resolver's mode.
func (r *Resolver) Mode() Mode {
	return r.mode
}

// Resolve decides the principal for a request. write reports whether the
// request mutates state.
func (r *Resolver) Resolve(ctx context.Context, creds Credentials, write bool) Decision {
	var d Decision
	asserted := Principal{}
	if creds.AssertedEmail != "" {
		asserted = Principal{Email: creds.AssertedEmail, Source: SourceAsserted}
	}
	if r.mode == ModeOff {
		d.Principal = asserted
		return d
	}

	// The first configured source that presents a credential decides; a
	// presented credential is never skipped in favor of a later source.
	switch {
	case creds.BearerToken != "" && r.sources.Tokens != nil:
		d.Verified, d.VerifyErr = verify(ctx, r.sources.Tokens, creds.BearerToken, SourceToken)
	case creds.ProxyJWT != "" && r.sources.Proxy != nil:
		d.Verified, d.VerifyErr = verify(ctx, r.sources.Proxy, creds.ProxyJWT, SourceProxyJWT)
	}
	d.Mismatch = d.Verified.Email != "" && asserted.Email != "" && d.Verified.Email != asserted.Email

	if r.mode == ModeShadow {
		d.Principal = asserted
		if asserted.Email == "" {
			d.Principal = d.Verified
		}
		return d
	}

	// ModeEnforce. A credential that was presented but failed is always
	// rejected rather than silently downgraded to a weaker source.
	if d.VerifyErr != nil {
		d.Deny = fmt.Errorf("%w: %w", ErrInvalidCredential, d.VerifyErr)
		return d
	}
	if d.Verified.Email != "" {
		d.Principal = d.Verified
		return d
	}
	if write || r.enforceOn == EnforceAll {
		d.Deny = ErrMissingCredential
		return d
	}
	// Out-of-scope reads keep asserted attribution for logs and history.
	d.Principal = asserted
	return d
}

type verifier interface {
	Verify(ctx context.Context, credential string) (string, error)
}

func verify(ctx context.Context, v verifier, credential string, source Source) (Principal, error) {
	email, err := v.Verify(ctx, credential)
	if err != nil {
		return Principal{}, err
	}
	return Principal{Email: email, Source: source, Verified: true}, nil
}

// NormalizeEmail returns the lowercase address, or "" when value is not a
// bare, plausible email address.
func NormalizeEmail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 254 {
		return ""
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return ""
	}
	at := strings.LastIndexByte(value, '@')
	if at <= 0 || at == len(value)-1 || !strings.Contains(value[at+1:], ".") {
		return ""
	}
	return strings.ToLower(value)
}
