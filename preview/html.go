package preview

import (
	"bytes"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// minParagraph is the shortest first paragraph worth using as a description;
// shorter ones are usually captions, bylines or button text.
const minParagraph = 40

// skipped elements never contribute text.
var skipped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true, atom.Svg: true,
}

// chrome elements hold navigation rather than content; their paragraphs are
// not used as a description.
var chrome = map[atom.Atom]bool{
	atom.Nav: true, atom.Header: true, atom.Footer: true, atom.Aside: true,
}

// summarizeHTML reads the title, description, h2 sections and trove:stat
// meta tags. Precedence: og:title, <title>, first <h1>; meta description,
// og:description, first substantial paragraph.
func summarizeHTML(doc []byte) Summary {
	s, _ := scanHTML(doc)
	return s
}

// scanHTML summarizes a page and reports which preview hints its author set.
func scanHTML(doc []byte) (Summary, Authored) {
	var (
		s                      Summary
		title, ogTitle, h1     string
		metaDesc, ogDesc, para string
		skipDepth, chromeDepth int
		slides, tabPanels      int  // panels shown one at a time
		hashNav                bool // a script listens for "hashchange"
		capturing              atom.Atom
		captureBuf             strings.Builder
	)
	z := html.NewTokenizer(bytes.NewReader(doc))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		tok := z.Token()
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			for _, a := range tok.Attr {
				switch {
				case a.Key == "class" && slices.Contains(strings.Fields(a.Val), "slide"):
					slides++
				case a.Key == "role" && a.Val == "tabpanel":
					tabPanels++
				}
			}
		}
		if tt == html.TextToken && skipDepth > 0 && strings.Contains(tok.Data, "hashchange") {
			hashNav = true
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			switch {
			case tok.DataAtom == atom.Meta:
				name, content := metaPair(tok)
				switch name {
				case "description":
					metaDesc = firstNonEmpty(metaDesc, content)
				case "og:description":
					ogDesc = firstNonEmpty(ogDesc, content)
				case "og:title":
					ogTitle = firstNonEmpty(ogTitle, content)
				case "trove:stat":
					if st, ok := parseStat(content); ok {
						s.Stats = append(s.Stats, st)
					}
				}
			case tt == html.SelfClosingTagToken:
			case skipped[tok.DataAtom]:
				skipDepth++
			case chrome[tok.DataAtom]:
				chromeDepth++
			case capturing == 0 && isCaptured(tok.DataAtom):
				capturing = tok.DataAtom
				captureBuf.Reset()
			}
		case html.EndTagToken:
			switch {
			case skipped[tok.DataAtom]:
				skipDepth = max(skipDepth-1, 0)
			case chrome[tok.DataAtom]:
				chromeDepth = max(chromeDepth-1, 0)
			case tok.DataAtom == capturing:
				text := squash(captureBuf.String())
				switch capturing {
				case atom.Title:
					title = firstNonEmpty(title, text)
				case atom.H1:
					h1 = firstNonEmpty(h1, text)
				case atom.H2:
					if text != "" {
						s.Sections = append(s.Sections, text)
					}
				case atom.P:
					if para == "" && chromeDepth == 0 && len([]rune(text)) >= minParagraph {
						para = text
					}
				}
				capturing = 0
			}
		case html.TextToken:
			if capturing != 0 && (skipDepth == 0 || capturing == atom.Title) {
				captureBuf.WriteString(tok.Data)
				captureBuf.WriteByte(' ')
			}
		}
	}
	s.Title = firstNonEmpty(ogTitle, title, h1)
	s.Description = firstNonEmpty(metaDesc, ogDesc, para)
	return s, Authored{
		Title:        firstNonEmpty(ogTitle, title) != "",
		Description:  firstNonEmpty(metaDesc, ogDesc) != "",
		Stats:        len(s.Stats),
		HiddenPanels: slides >= 2 || tabPanels > 0,
		HashNav:      hashNav,
	}
}

func isCaptured(a atom.Atom) bool {
	return a == atom.Title || a == atom.H1 || a == atom.H2 || a == atom.P
}

// metaPair returns a meta tag's name (or property) and content.
func metaPair(tok html.Token) (name, content string) {
	for _, attr := range tok.Attr {
		switch strings.ToLower(attr.Key) {
		case "name", "property":
			name = strings.ToLower(strings.TrimSpace(attr.Val))
		case "content":
			content = attr.Val
		}
	}
	return name, squash(content)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = squash(v); v != "" {
			return v
		}
	}
	return ""
}
