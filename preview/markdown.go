package preview

import (
	"bytes"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// wordsPerMinute estimates reading time.
const wordsPerMinute = 220

// frontmatter is the optional YAML block at the top of a Markdown file.
type frontmatter struct {
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Stats       []string `yaml:"stats"` // "Label: Value"
}

var (
	mdImage    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLink     = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	mdEmphasis = regexp.MustCompile("(\\*\\*|__|~~|`|\\*)")
	mdHTMLTag  = regexp.MustCompile(`<[^>]+>`)
)

// summarizeMarkdown reads frontmatter, the first # heading, the first
// paragraph and ## headings, skipping fenced code.
func summarizeMarkdown(doc []byte) Summary {
	s, _ := scanMarkdown(doc)
	return s
}

// scanMarkdown summarizes a document and reports which preview hints its
// author set.
func scanMarkdown(doc []byte) (Summary, Authored) {
	body, fm := splitFrontmatter(doc)
	var (
		s                 Summary
		h1                string
		para              []string
		paraDone, inFence bool
		fence             string
		diagrams, words   int
	)
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if marker, ok := fenceMarker(trimmed); ok {
			switch {
			case !inFence:
				inFence, fence = true, marker
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "`~"))), "mermaid") {
					diagrams++
				}
			case strings.HasPrefix(trimmed, fence):
				inFence = false
			}
			continue
		}
		if inFence {
			continue
		}
		words += len(strings.Fields(trimmed))
		switch {
		case strings.HasPrefix(trimmed, "# "):
			if h1 == "" {
				h1 = plainMarkdown(trimmed[2:])
			}
			if len(para) > 0 {
				paraDone = true
			}
		case strings.HasPrefix(trimmed, "## "):
			if text := plainMarkdown(trimmed[3:]); text != "" {
				s.Sections = append(s.Sections, text)
			}
			if len(para) > 0 {
				paraDone = true
			}
		case trimmed == "":
			if len(para) > 0 {
				paraDone = true
			}
		case paraDone || !isProse(trimmed):
			if len(para) > 0 {
				paraDone = true
			}
		default:
			para = append(para, trimmed)
		}
	}
	for _, raw := range fm.Stats {
		if st, ok := parseStat(raw); ok {
			s.Stats = append(s.Stats, st)
		}
	}
	s.Title = firstNonEmpty(fm.Title, h1)
	s.Description = firstNonEmpty(fm.Description, plainMarkdown(strings.Join(para, " ")))
	if minutes := (words + wordsPerMinute - 1) / wordsPerMinute; words > 0 {
		s.Facts = append(s.Facts, plural(minutes, "min read", "min read"))
	}
	if diagrams > 0 {
		s.Facts = append(s.Facts, plural(diagrams, "diagram", "diagrams"))
	}
	return s, Authored{
		Title:       firstNonEmpty(fm.Title, h1) != "",
		Description: squash(fm.Description) != "",
		Stats:       len(s.Stats),
	}
}

// splitFrontmatter separates a leading "---" YAML block. Malformed
// frontmatter is dropped rather than failing the preview.
func splitFrontmatter(doc []byte) ([]byte, frontmatter) {
	var fm frontmatter
	rest, ok := bytes.CutPrefix(bytes.TrimPrefix(doc, []byte("\ufeff")), []byte("---\n"))
	if !ok {
		return doc, fm
	}
	block, body, ok := bytes.Cut(rest, []byte("\n---"))
	if !ok {
		return doc, fm
	}
	_, body, _ = bytes.Cut(body, []byte("\n"))
	if err := yaml.Unmarshal(block, &fm); err != nil {
		return body, frontmatter{}
	}
	return body, fm
}

func fenceMarker(line string) (string, bool) {
	for _, m := range []string{"```", "~~~"} {
		if strings.HasPrefix(line, m) {
			return m, true
		}
	}
	return "", false
}

// isProse reports whether a line can start or continue a paragraph: not a
// heading, list, quote, table, rule or raw HTML block.
func isProse(line string) bool {
	switch {
	case strings.HasPrefix(line, "#"), strings.HasPrefix(line, ">"), strings.HasPrefix(line, "|"),
		strings.HasPrefix(line, "<"), strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "),
		strings.HasPrefix(line, "+ "), line == "---", line == "***":
		return false
	}
	if i := strings.Index(line, ". "); i > 0 && i <= 3 && strings.Trim(line[:i], "0123456789") == "" {
		return false
	}
	return true
}

// plainMarkdown strips inline Markdown so text reads cleanly in a preview.
func plainMarkdown(s string) string {
	s = mdImage.ReplaceAllString(s, "$1")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdHTMLTag.ReplaceAllString(s, "")
	s = mdEmphasis.ReplaceAllString(s, "")
	return squash(s)
}
