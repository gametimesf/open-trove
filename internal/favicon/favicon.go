// Package favicon renders Trove's "T cube" favicon: an isometric cube with a T cut into each
// visible face (front, side, top). Each face's T is rotated independently, the cube is filled
// with a two-stop gradient, and the T regions are slightly darkened so they read as letters.
package favicon

import (
	"math"
	"strconv"
	"strings"
)

// Rotation is a clockwise quarter-turn count (0-3) applied to a face's T.
type Rotation int

// RotationCount is the number of distinct rotations per face.
const RotationCount = 4

// Palette is a named two-stop gradient.
type Palette struct {
	Name string
	From string
	To   string
}

// Palettes are the gradients a random favicon is drawn from. Very dark gradients are excluded
// because the favicon has no background and they disappear on dark tab bars.
var Palettes = []Palette{
	{"Ember", "#ff8a3d", "#c2183c"}, {"Gold", "#ffd75e", "#e0861a"}, {"Aurora", "#29e3b0", "#4d5cff"},
	{"Grape", "#b35cff", "#5b21d6"}, {"Lagoon", "#35d6ff", "#2f6fe0"}, {"Rose", "#ff6b9d", "#c21d6b"},
	{"Lime", "#c4f25a", "#1fa36a"}, {"Sunset", "#ffb04a", "#ff4f6d"}, {"Mint", "#7af0cf", "#11998e"},
	{"Cobalt", "#5aa7ff", "#2a3bd6"}, {"Orchid", "#f59bff", "#9b2fe0"}, {"Neon", "#fff36b", "#ff3ca6"},
	{"Ocean", "#2ee6d6", "#1a5cb8"}, {"Plum", "#ff7a7a", "#7a2a8f"}, {"Teal", "#33d1a0", "#0b6e78"},
	{"Copper", "#f2a65a", "#8c3b1f"},
}

// DefaultContrast is the opacity of the darkening overlay on each T.
const DefaultContrast = 0.22

// Spec fully determines a rendered favicon.
type Spec struct {
	Front    Rotation
	Side     Rotation
	Top      Rotation
	Palette  Palette
	Contrast float64 // 0 disables the T darkening
}

// Fallback is the fixed design behind the static PNG icons served to browsers without SVG
// favicon support. Regenerate the PNGs (see assets.go) whenever it or the geometry changes.
var Fallback = Spec{Palette: Palette{"Teal", "#33d1a0", "#0b6e78"}, Contrast: DefaultContrast}

// Random picks rotations and a palette using intn, which must return a value in [0, n).
// Callers inject the source (e.g. math/rand/v2.IntN) so tests can be deterministic.
func Random(intn func(n int) int) Spec {
	return Spec{
		Front:    Rotation(intn(RotationCount)),
		Side:     Rotation(intn(RotationCount)),
		Top:      Rotation(intn(RotationCount)),
		Palette:  Palettes[intn(len(Palettes))],
		Contrast: DefaultContrast,
	}
}

// Cube geometry, in face units. Faces are drawn in 2:1 isometric projection.
const (
	edge    = 11.0 // cube edge length
	lid     = 4.0  // distance from a face's top edge to the T crossbar seam
	stem    = 4.0  // width of the T stem
	fitSize = 26.0 // the cube's 22x22 projected bounds are scaled to this
	lineW   = 1.5  // cut width in output (viewBox) units
)

var (
	scale = fitSize / (2 * edge)
	// cut is the cut width in face units; slant compensates for lines running along a slanted axis,
	// so every cut has the same perpendicular weight on screen.
	cut   = lineW / scale
	slant = math.Hypot(1, .5)

	frontFace = "matrix(1 .5 0 1 0 0)"
	sideFace  = "matrix(1 -.5 0 1 " + num(edge) + " " + num(edge/2) + ")"
	topFace   = "matrix(1 .5 1 -.5 0 0)"
	// The projected cube spans x 0..2*edge and y -edge/2..1.5*edge; center it in the viewBox.
	placement = "translate(16 16) scale(" + num(scale) + ") translate(" + num(-edge) + " " + num(-edge/2) + ")"
)

// SVG renders the spec as a standalone 32x32 SVG document.
func SVG(s Spec) string {
	var ink, cuts, regions strings.Builder
	for _, f := range []string{frontFace, sideFace, topFace} {
		rect(&ink, f, 0, 0, edge, edge, "")
	}

	// Box edges: top-front and top-side seams (slanted) plus the vertical front/side corner.
	rect(&cuts, frontFace, -1, -cut*slant/2, edge+1, cut*slant, black)
	rect(&cuts, sideFace, 0, -cut*slant/2, edge+1, cut*slant, black)
	rect(&cuts, "", edge-cut/2, edge/2, cut, edge+1, black)

	faces := []struct {
		transform string
		rot       Rotation
		alongX    float64 // thickness factor for cuts running along the face's x axis
		alongY    float64 // thickness factor for cuts running along the face's y axis
	}{
		{frontFace, s.Front, slant, 1},
		{sideFace, s.Side, slant, 1},
		{topFace, s.Top, slant, slant},
	}
	for _, f := range faces {
		faceCuts(&cuts, f.transform, f.rot, f.alongX, f.alongY)
		faceRegions(&regions, f.transform, f.rot)
	}

	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">`)
	b.WriteString(`<defs><linearGradient id="g" gradientUnits="userSpaceOnUse" x1="0" y1="0" x2="32" y2="32">`)
	b.WriteString(`<stop offset="0" stop-color="` + s.Palette.From + `"/><stop offset="1" stop-color="` + s.Palette.To + `"/></linearGradient>`)
	b.WriteString(`<mask id="m" maskUnits="userSpaceOnUse" x="0" y="0" width="32" height="32"><g transform="` + placement + `" fill="#fff">`)
	b.WriteString(ink.String() + cuts.String() + `</g></mask>`)
	if s.Contrast > 0 {
		b.WriteString(`<mask id="t" maskUnits="userSpaceOnUse" x="0" y="0" width="32" height="32"><g transform="` + placement + `" fill="#fff">`)
		b.WriteString(regions.String() + `</g></mask>`)
	}
	b.WriteString(`</defs><rect width="32" height="32" fill="url(#g)" mask="url(#m)"/>`)
	if s.Contrast > 0 {
		b.WriteString(`<g mask="url(#t)"><rect width="32" height="32" fill="#000" fill-opacity="` + num(s.Contrast) + `" mask="url(#m)"/></g>`)
	}
	b.WriteString(`</svg>`)
	return b.String()
}

const black = "#000"

// faceCuts writes the T outline for one face: the crossbar seam on either side of the stem plus
// the two stem edges. Segments are rotated in face space, then emitted as axis-aligned rects whose
// ends overlap so inside corners stay sharp.
func faceCuts(b *strings.Builder, transform string, r Rotation, alongX, alongY float64) {
	left, right := (edge-stem)/2, (edge+stem)/2
	segments := [][4]float64{
		{0, lid, left, lid}, {left, lid, left, edge},
		{right, lid, edge, lid}, {right, lid, right, edge},
	}
	for _, sg := range segments {
		x1, y1 := rotate(sg[0], sg[1], r)
		x2, y2 := rotate(sg[2], sg[3], r)
		if y1 == y2 {
			rect(b, transform, math.Min(x1, x2)-cut*alongY/2, y1-cut*alongX/2, math.Abs(x2-x1)+cut*alongY, cut*alongX, black)
		} else {
			rect(b, transform, x1-cut*alongY/2, math.Min(y1, y2)-cut*alongX/2, cut*alongY, math.Abs(y2-y1)+cut*alongX, black)
		}
	}
}

// faceRegions writes the solid T (lid band plus stem) for one face, used to darken it.
func faceRegions(b *strings.Builder, transform string, r Rotation) {
	left := (edge - stem) / 2
	for _, rc := range [][4]float64{{0, 0, edge, lid}, {left, lid, stem, edge - lid}} {
		x1, y1 := rotate(rc[0], rc[1], r)
		x2, y2 := rotate(rc[0]+rc[2], rc[1]+rc[3], r)
		rect(b, transform, math.Min(x1, x2), math.Min(y1, y2), math.Abs(x2-x1), math.Abs(y2-y1), "")
	}
}

// rotate turns a face-space point clockwise by r quarter turns about the face center.
func rotate(x, y float64, r Rotation) (float64, float64) {
	for range int(r) % RotationCount {
		x, y = edge-y, x
	}
	return x, y
}

func rect(b *strings.Builder, transform string, x, y, w, h float64, fill string) {
	b.WriteString(`<rect`)
	if transform != "" {
		b.WriteString(` transform="` + transform + `"`)
	}
	b.WriteString(` x="` + num(x) + `" y="` + num(y) + `" width="` + num(w) + `" height="` + num(h) + `"`)
	if fill != "" {
		b.WriteString(` fill="` + fill + `"`)
	}
	b.WriteString(`/>`)
}

// num formats a coordinate compactly with three decimals of precision.
func num(v float64) string {
	return strconv.FormatFloat(math.Round(v*1000)/1000, 'f', -1, 64)
}
