// Package preview summarizes artifact content for link previews: a title, a
// short description, section headings, highlighted stats and a small excerpt.
// It reads only the bytes it is given and never fetches anything.
package preview

import (
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxBytes is how much of an artifact Summarize needs. Callers read at most
// MaxBytes+1 bytes so Summarize can tell whether the content was cut off.
const MaxBytes = 256 << 10

// Limits on what a summary carries, sized for a chat preview.
const (
	maxTitle       = 150
	maxDescription = 300
	maxSections    = 6
	maxStats       = 6
	maxStatLabel   = 40
	maxStatValue   = 60
	maxSnippetRows = 8
	maxSnippetCols = 72
)

// Kind is a broad artifact category.
type Kind string

// Artifact kinds.
const (
	KindHTML     Kind = "html"
	KindMarkdown Kind = "markdown"
	KindSite     Kind = "site"
	KindCSV      Kind = "csv"
	KindJSON     Kind = "json"
	KindCode     Kind = "code"
	KindText     Kind = "text"
	KindImage    Kind = "image"
	KindVideo    Kind = "video"
	KindPDF      Kind = "pdf"
	KindDocument Kind = "document"
	KindFile     Kind = "file"
)

var kindLabels = map[Kind]string{
	KindHTML:     "HTML page",
	KindMarkdown: "Markdown",
	KindSite:     "Website",
	KindCSV:      "CSV",
	KindJSON:     "JSON",
	KindCode:     "Code",
	KindText:     "Text",
	KindImage:    "Image",
	KindVideo:    "Video",
	KindPDF:      "PDF",
	KindDocument: "Word document",
	KindFile:     "File",
}

// Label is a human-readable name for the kind.
func (k Kind) Label() string {
	if l, ok := kindLabels[k]; ok {
		return l
	}
	return kindLabels[KindFile]
}

// Stat is a highlighted figure an author chose to surface, e.g. "Revenue: $1.2M".
type Stat struct {
	Label string
	Value string
}

// Summary is what a preview can show about an artifact. Empty fields are omitted.
type Summary struct {
	Kind        Kind
	Title       string
	Description string
	Sections    []string // top-level headings, in document order
	Stats       []Stat
	Facts       []string // short facts such as "4 min read" or "1,200 rows × 8 columns"
	Snippet     string   // monospace excerpt: table head, first lines of code
}

// Summarize builds a summary from up to MaxBytes+1 leading bytes of an
// artifact. Content beyond MaxBytes is treated as truncated.
func Summarize(contentType, filename string, head []byte) Summary {
	truncated := len(head) > MaxBytes
	if truncated {
		head = head[:MaxBytes]
	}
	kind := kindOf(contentType, filename)
	var s Summary
	switch kind {
	case KindHTML:
		s = summarizeHTML(head)
	case KindMarkdown:
		s = summarizeMarkdown(head)
	case KindCSV:
		s = summarizeCSV(head, truncated)
	case KindJSON:
		s = summarizeJSON(head, truncated)
	case KindCode, KindText:
		s = summarizeText(head, truncated)
	case KindImage:
		s = summarizeImage(head)
	}
	s.Kind = kind
	if kind == KindCode {
		if lang := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), "."); lang != "" {
			s.Facts = append([]string{strings.ToUpper(lang)}, s.Facts...)
		}
	}
	return s.bounded()
}

// SummarizeSite summarizes a multi-page site from its entry page.
func SummarizeSite(entry []byte, fileCount int) Summary {
	s := summarizeHTML(entry)
	s.Kind = KindSite
	if fileCount > 0 {
		s.Facts = append(s.Facts, plural(fileCount, "file", "files"))
	}
	return s.bounded()
}

var codeExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true, ".rb": true,
	".java": true, ".kt": true, ".swift": true, ".rs": true, ".c": true, ".h": true, ".cpp": true,
	".cs": true, ".php": true, ".sh": true, ".bash": true, ".sql": true, ".yaml": true, ".yml": true,
	".toml": true, ".tf": true, ".hcl": true, ".proto": true, ".graphql": true, ".css": true,
	".scss": true, ".xml": true, ".ini": true, ".dockerfile": true, ".mermaid": true, ".mmd": true,
}

func kindOf(contentType, filename string) Kind {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	ext := strings.ToLower(filepath.Ext(filename))
	switch {
	case ct == "text/html" || ext == ".html" || ext == ".htm":
		return KindHTML
	case ct == "text/markdown" || ext == ".md" || ext == ".markdown":
		return KindMarkdown
	case ct == "text/csv" || ext == ".csv":
		return KindCSV
	case ct == "application/json" || ext == ".json":
		return KindJSON
	case strings.HasPrefix(ct, "image/"):
		return KindImage
	case strings.HasPrefix(ct, "video/"):
		return KindVideo
	case ct == "application/pdf":
		return KindPDF
	case ct == "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return KindDocument
	case codeExtensions[ext]:
		return KindCode
	case strings.HasPrefix(ct, "text/"):
		return KindText
	default:
		return KindFile
	}
}

func (s Summary) bounded() Summary {
	s.Title = clip(s.Title, maxTitle)
	s.Description = clip(s.Description, maxDescription)
	if len(s.Sections) > maxSections {
		s.Sections = s.Sections[:maxSections]
	}
	if len(s.Stats) > maxStats {
		s.Stats = s.Stats[:maxStats]
	}
	return s
}

// parseStat splits "Label: Value"; values without a label are dropped.
func parseStat(raw string) (Stat, bool) {
	label, value, ok := strings.Cut(raw, ":")
	label, value = squash(label), squash(value)
	if !ok || label == "" || value == "" {
		return Stat{}, false
	}
	return Stat{Label: clip(label, maxStatLabel), Value: clip(value, maxStatValue)}, true
}

// squash collapses runs of whitespace into single spaces and trims the ends.
func squash(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

// clip shortens s to at most n runes, ending with an ellipsis when cut.
func clip(s string, n int) string {
	s = squash(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	cut := strings.TrimRightFunc(string(r[:n-1]), func(c rune) bool { return unicode.IsSpace(c) || unicode.IsPunct(c) })
	return cut + "…"
}

// clipLine shortens one line of an excerpt without collapsing its spacing.
func clipLine(s string, n int) string {
	s = strings.TrimRightFunc(strings.ReplaceAll(s, "\t", "    "), unicode.IsSpace)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return formatInt(n) + " " + many
}

// formatInt renders a non-negative count with thousands separators.
func formatInt(n int) string {
	s := strconv.Itoa(n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
