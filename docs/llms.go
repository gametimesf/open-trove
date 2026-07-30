// Package docs exposes static API documentation bundled with the server.
package docs

import "embed"

// Files contains the static discovery documents served by Trove.
//
//go:embed llms.txt
var Files embed.FS
