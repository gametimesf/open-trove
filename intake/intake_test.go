package intake

import (
	"context"
	"testing"
)

func TestNoOpAllowsEverything(t *testing.T) {
	var insp Inspector = NoOp{}
	cases := []Input{
		{Filename: "a.md", ContentType: "text/markdown", Body: []byte("# hi")},
		{Filename: "x.png", ContentType: "image/png", Body: []byte("PNG…")},
		{},
	}
	for _, in := range cases {
		v, err := insp.Inspect(context.Background(), in)
		if err != nil {
			t.Errorf("err: %v", err)
		}
		if v == nil || !v.Allowed {
			t.Errorf("NoOp must allow: %+v", v)
		}
	}
}

func TestFailModeConstants(t *testing.T) {
	if FailClosed == FailOpen {
		t.Error("constants collided")
	}
}
