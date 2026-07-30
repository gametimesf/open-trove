package intake

import "context"

// NoOp is an Inspector that allows everything. Callers use it when the
// feature is disabled so the rest of the upload path doesn't have to
// special-case a nil inspector.
type NoOp struct{}

// Inspect always returns Allowed=true.
func (NoOp) Inspect(_ context.Context, _ Input) (*Verdict, error) {
	return &Verdict{Allowed: true}, nil
}
