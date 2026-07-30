package intake

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// InspectFile is a convenience for off-server scripts: read a file
// from disk, infer its content type from extension + magic bytes, and
// run it through the supplied Inspector.
//
// Example:
//
//	insp, _ := intake.NewAnthropic(intake.AnthropicConfig{...})
//	v, err := intake.InspectFile(ctx, insp, "report.md")
//
// The exported nature of this function (and the Inspector interface)
// is the whole reason the package lives outside `internal/`.
func InspectFile(ctx context.Context, insp Inspector, path string) (*Verdict, error) {
	if insp == nil {
		return nil, fmt.Errorf("intake: nil inspector")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("intake: read %s: %w", path, err)
	}
	ct := detectContentType(path, body)
	return insp.Inspect(ctx, Input{
		Filename:    filepath.Base(path),
		ContentType: ct,
		Body:        body,
	})
}

// detectContentType picks a content type from the filename extension
// (strongest signal) with a fallback to http.DetectContentType on the
// first 512 bytes. Roughly mirrors trove's upload-side detection so
// scanner verdicts and live verdicts behave the same way.
func detectContentType(path string, body []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ct := extContentTypes[ext]; ct != "" {
		return ct
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	if len(body) > 0 {
		return http.DetectContentType(firstN(body, 512))
	}
	return "application/octet-stream"
}

// extContentTypes mirrors the override map in cmd/server's handler so
// known-tricky cases (HTML, JS, MD, etc.) match between the two.
var extContentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".htm":  "text/html; charset=utf-8",
	".md":   "text/markdown; charset=utf-8",
	".txt":  "text/plain; charset=utf-8",
	".log":  "text/plain; charset=utf-8",
	".csv":  "text/csv; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".pdf":  "application/pdf",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".zip":  "application/zip",
}

func firstN(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
