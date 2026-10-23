package favicon

import _ "embed"

// Static PNG renders of Fallback for browsers without SVG favicon support (and iOS home screens).
// To regenerate: write SVG(Fallback) to fallback.svg, then screenshot it with headless Chrome at
// 32x32 (transparent background) and 180x180 (white background, since iOS fills transparency
// with black), e.g.
//
//	chrome --headless=new --force-device-scale-factor=1 --default-background-color=00000000 \
//	  --window-size=32,32 --screenshot=favicon-32.png page-with-fallback-img.html
var (
	//go:embed static/favicon-32.png
	PNG32 []byte

	//go:embed static/apple-touch-icon.png
	AppleTouchIcon []byte
)
