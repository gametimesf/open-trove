package favicon

import (
	"encoding/xml"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
)

// sequence returns an intn func that yields the given values in order.
func sequence(t *testing.T, values ...int) func(int) int {
	t.Helper()
	i := 0
	return func(n int) int {
		if i >= len(values) {
			t.Fatalf("intn called more than %d times", len(values))
		}
		v := values[i]
		i++
		if v < 0 || v >= n {
			t.Fatalf("test value %d out of range [0,%d)", v, n)
		}
		return v
	}
}

func TestRandom(t *testing.T) {
	tests := []struct {
		name   string
		values []int
		want   Spec
	}{
		{
			name:   "first of everything",
			values: []int{0, 0, 0, 0},
			want:   Spec{Front: 0, Side: 0, Top: 0, Palette: Palettes[0], Contrast: DefaultContrast},
		},
		{
			name:   "mixed rotations and last palette",
			values: []int{1, 2, 3, len(Palettes) - 1},
			want:   Spec{Front: 1, Side: 2, Top: 3, Palette: Palettes[len(Palettes)-1], Contrast: DefaultContrast},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Random(sequence(t, tt.values...)); got != tt.want {
				t.Errorf("Random() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRandomStaysInRange(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 1000 {
		s := Random(r.IntN)
		for _, rot := range []Rotation{s.Front, s.Side, s.Top} {
			if rot < 0 || rot >= RotationCount {
				t.Fatalf("rotation %d out of range", rot)
			}
		}
		if s.Palette.From == "" || s.Palette.To == "" {
			t.Fatalf("empty palette: %+v", s.Palette)
		}
	}
}

func TestSVG(t *testing.T) {
	ember := Palettes[0]
	tests := []struct {
		name        string
		spec        Spec
		contains    []string
		notContains []string
	}{
		{
			name:     "gradient uses palette colors",
			spec:     Spec{Palette: ember, Contrast: DefaultContrast},
			contains: []string{`viewBox="0 0 32 32"`, `stop-color="` + ember.From + `"`, `stop-color="` + ember.To + `"`},
		},
		{
			name:     "contrast adds darkened T layer",
			spec:     Spec{Palette: ember, Contrast: DefaultContrast},
			contains: []string{`<mask id="t"`, `fill-opacity="0.22"`},
		},
		{
			name:        "zero contrast omits T layer",
			spec:        Spec{Palette: ember},
			notContains: []string{`<mask id="t"`, `fill-opacity`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SVG(tt.spec)
			assertWellFormed(t, got)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("SVG() missing %q", want)
				}
			}
			for _, unwanted := range tt.notContains {
				if strings.Contains(got, unwanted) {
					t.Errorf("SVG() unexpectedly contains %q", unwanted)
				}
			}
		})
	}
}

func TestSVGRotationsAreDistinct(t *testing.T) {
	seen := make(map[string]Spec, RotationCount*RotationCount*RotationCount)
	for f := range Rotation(RotationCount) {
		for s := range Rotation(RotationCount) {
			for top := range Rotation(RotationCount) {
				spec := Spec{Front: f, Side: s, Top: top, Palette: Palettes[0], Contrast: DefaultContrast}
				svg := SVG(spec)
				assertWellFormed(t, svg)
				if prev, ok := seen[svg]; ok {
					t.Fatalf("%+v renders identically to %+v", spec, prev)
				}
				seen[svg] = spec
			}
		}
	}
}

func TestRotate(t *testing.T) {
	tests := []struct {
		name         string
		r            Rotation
		wantX, wantY float64
	}{
		{"none", 0, 2, 3},
		{"quarter", 1, edge - 3, 2},
		{"half", 2, edge - 2, edge - 3},
		{"three quarters", 3, 3, edge - 2},
		{"full turn wraps", 4, 2, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x, y := rotate(2, 3, tt.r)
			if x != tt.wantX || y != tt.wantY {
				t.Errorf("rotate(2, 3, %d) = (%v, %v), want (%v, %v)", tt.r, x, y, tt.wantX, tt.wantY)
			}
		})
	}
}

func assertWellFormed(t *testing.T, doc string) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(doc))
	for {
		_, err := d.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("SVG is not well-formed XML: %v", err)
		}
	}
}
