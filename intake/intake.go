// Package intake provides an inspection pass that runs over upload
// content before it is persisted by Trove. The Inspector interface lets
// callers swap implementations (a real LLM-backed inspector for
// production, a NoOp for disabled mode, or a fake for tests).
//
// The package is intentionally public (not under internal/) so an
// operator can import it from a small standalone tool that audits
// arbitrary files outside the running server.
package intake

import (
	"context"
	"errors"
)

// Verdict is what the inspector returns for a given input.
//
// When Allowed is false, Reason should be a short human-readable
// explanation (suitable for showing to the uploader) and Categories
// may carry a free-form tag set the prompt has decided on.
type Verdict struct {
	Allowed    bool     `json:"allowed"`
	Reason     string   `json:"reason,omitempty"`
	Categories []string `json:"categories,omitempty"`
}

// Input is the unit of inspection: one file's bytes plus enough
// metadata for the inspector to classify how to read them.
type Input struct {
	Filename    string
	ContentType string
	Body        []byte
}

// Inspector is the contract every inspection backend satisfies. The
// public NoOp lives in this package; LLM-backed implementations live
// alongside (e.g. AnthropicInspector). Test code can supply its own.
type Inspector interface {
	Inspect(ctx context.Context, in Input) (*Verdict, error)
}

// FailMode controls what callers do when an Inspector returns an
// error. It's not an Inspector concern itself — the Inspector just
// reports failures; the caller decides whether to block or pass through.
type FailMode string

// FailMode values.
const (
	FailClosed FailMode = "closed" // reject the upload on inspector error (safer)
	FailOpen   FailMode = "open"   // allow the upload on inspector error (more available)
)

// ErrInspector is the sentinel callers can use to recognize
// inspection-side failures (HTTP, JSON, timeout, etc.) versus
// content-policy verdicts.
var ErrInspector = errors.New("intake: inspector failed")
