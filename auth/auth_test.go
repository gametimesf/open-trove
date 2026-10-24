package auth

import (
	"context"
	"errors"
	"testing"
)

// stubVerifier returns a fixed result for one expected token.
type stubVerifier struct {
	token string
	email string
	err   error
}

func (s stubVerifier) Verify(_ context.Context, token string) (string, error) {
	if token != s.token {
		return "", errors.New("unexpected token")
	}
	return s.email, s.err
}

var errBadToken = errors.New("bad token")

func TestResolve(t *testing.T) {
	good := stubVerifier{token: "good", email: "verified@example.com"}
	bad := stubVerifier{token: "bad", err: errBadToken}

	tests := []struct {
		name          string
		mode          Mode
		enforceOn     EnforceOn
		verifier      ProxyVerifier
		creds         Credentials
		write         bool
		wantEmail     string
		wantSource    Source
		wantVerified  string
		wantMismatch  bool
		wantVerifyErr bool
		wantDeny      error
	}{
		{
			name: "off uses asserted and ignores jwt", mode: ModeOff, verifier: good,
			creds: Credentials{ProxyJWT: "good", AssertedEmail: "asserted@example.com"}, write: true,
			wantEmail: "asserted@example.com", wantSource: SourceAsserted,
		},
		{
			name: "off without verifier is valid", mode: ModeOff,
			creds:     Credentials{AssertedEmail: "asserted@example.com"},
			wantEmail: "asserted@example.com", wantSource: SourceAsserted,
		},
		{
			name: "shadow keeps asserted and reports verified mismatch", mode: ModeShadow, verifier: good,
			creds: Credentials{ProxyJWT: "good", AssertedEmail: "asserted@example.com"}, write: true,
			wantEmail: "asserted@example.com", wantSource: SourceAsserted,
			wantVerified: "verified@example.com", wantMismatch: true,
		},
		{
			name: "shadow does not block invalid jwt", mode: ModeShadow, verifier: bad,
			creds: Credentials{ProxyJWT: "bad", AssertedEmail: "asserted@example.com"}, write: true,
			wantEmail: "asserted@example.com", wantSource: SourceAsserted, wantVerifyErr: true,
		},
		{
			name: "enforce uses verified over asserted", mode: ModeEnforce, enforceOn: EnforceWrites, verifier: good,
			creds: Credentials{ProxyJWT: "good", AssertedEmail: "asserted@example.com"}, write: true,
			wantEmail: "verified@example.com", wantSource: SourceProxyJWT,
			wantVerified: "verified@example.com", wantMismatch: true,
		},
		{
			name: "enforce rejects write without credential", mode: ModeEnforce, enforceOn: EnforceWrites, verifier: good,
			creds: Credentials{AssertedEmail: "asserted@example.com"}, write: true,
			wantDeny: ErrMissingCredential,
		},
		{
			name: "enforce rejects invalid credential even on read", mode: ModeEnforce, enforceOn: EnforceWrites, verifier: bad,
			creds: Credentials{ProxyJWT: "bad"}, write: false,
			wantVerifyErr: true, wantDeny: ErrInvalidCredential,
		},
		{
			name: "enforce writes keeps asserted attribution on reads", mode: ModeEnforce, enforceOn: EnforceWrites, verifier: good,
			creds:     Credentials{AssertedEmail: "asserted@example.com"},
			wantEmail: "asserted@example.com", wantSource: SourceAsserted,
		},
		{
			name: "enforce all rejects read without credential", mode: ModeEnforce, enforceOn: EnforceAll, verifier: good,
			creds:    Credentials{AssertedEmail: "asserted@example.com"},
			wantDeny: ErrMissingCredential,
		},
		{
			name: "matching emails are not a mismatch", mode: ModeEnforce, enforceOn: EnforceWrites, verifier: good,
			creds: Credentials{ProxyJWT: "good", AssertedEmail: "verified@example.com"}, write: true,
			wantEmail: "verified@example.com", wantSource: SourceProxyJWT, wantVerified: "verified@example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enforceOn := tt.enforceOn
			if enforceOn == "" {
				enforceOn = EnforceWrites
			}
			r, err := NewResolver(tt.mode, enforceOn, Sources{Proxy: tt.verifier})
			if err != nil {
				t.Fatalf("NewResolver: %v", err)
			}
			d := r.Resolve(context.Background(), tt.creds, tt.write)

			if tt.wantDeny != nil {
				if !errors.Is(d.Deny, tt.wantDeny) {
					t.Fatalf("Deny = %v, want %v", d.Deny, tt.wantDeny)
				}
				if d.Principal.Email != "" {
					t.Fatalf("denied request has principal %q", d.Principal.Email)
				}
			} else if d.Deny != nil {
				t.Fatalf("unexpected Deny: %v", d.Deny)
			}
			if d.Principal.Email != tt.wantEmail || d.Principal.Source != tt.wantSource {
				t.Errorf("Principal = %+v, want email %q source %q", d.Principal, tt.wantEmail, tt.wantSource)
			}
			if d.Principal.Verified != (tt.wantSource == SourceProxyJWT) {
				t.Errorf("Principal.Verified = %v for source %q", d.Principal.Verified, d.Principal.Source)
			}
			if d.Verified.Email != tt.wantVerified {
				t.Errorf("Verified = %q, want %q", d.Verified.Email, tt.wantVerified)
			}
			if d.Mismatch != tt.wantMismatch {
				t.Errorf("Mismatch = %v, want %v", d.Mismatch, tt.wantMismatch)
			}
			if (d.VerifyErr != nil) != tt.wantVerifyErr {
				t.Errorf("VerifyErr = %v, want error: %v", d.VerifyErr, tt.wantVerifyErr)
			}
		})
	}
}

func TestNewResolverValidation(t *testing.T) {
	good := stubVerifier{token: "good", email: "verified@example.com"}
	tests := []struct {
		name      string
		mode      Mode
		enforceOn EnforceOn
		verifier  ProxyVerifier
		wantErr   bool
	}{
		{"off without verifier", ModeOff, EnforceWrites, nil, false},
		{"shadow requires verifier", ModeShadow, EnforceWrites, nil, true},
		{"enforce requires verifier", ModeEnforce, EnforceWrites, nil, true},
		{"enforce with verifier", ModeEnforce, EnforceAll, good, false},
		{"unknown mode", Mode("strict"), EnforceWrites, good, true},
		{"unknown scope", ModeEnforce, EnforceOn("reads"), good, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewResolver(tt.mode, tt.enforceOn, Sources{Proxy: tt.verifier})
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewResolver() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	tests := []struct{ in, want string }{
		{" Person@Example.COM ", "person@example.com"},
		{"Name <person@example.com>", ""},
		{"person@localhost", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := NormalizeEmail(tt.in); got != tt.want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
