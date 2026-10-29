package preview

// Authored records which preview hints an author set explicitly, as opposed
// to what Trove derives from the content.
type Authored struct {
	Title       bool // <title>/og:title, or a frontmatter title or "# " heading
	Description bool // meta/og description, or frontmatter description
	Stats       int  // trove:stat tags or frontmatter stats
}

// Hint is a suggestion for making an artifact preview better.
type Hint struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Hint codes.
const (
	HintTitle       = "preview_title"
	HintDescription = "preview_description"
	HintStats       = "preview_stats"
	HintCard        = "preview_card"
)

// Hints suggests preview improvements for HTML and Markdown artifacts (and
// sites, whose entry page is HTML). Other kinds get none. hasCard reports
// whether a preview card is already set.
func Hints(contentType, filename string, head []byte, hasCard bool) []Hint {
	var a Authored
	switch kindOf(contentType, filename) {
	case KindHTML:
		_, a = scanHTML(head)
	case KindMarkdown:
		_, a = scanMarkdown(head)
	default:
		return nil
	}
	return hintsFor(a, hasCard)
}

// SiteHints suggests preview improvements for a site from its entry page.
func SiteHints(entry []byte, hasCard bool) []Hint {
	_, a := scanHTML(entry)
	return hintsFor(a, hasCard)
}

func hintsFor(a Authored, hasCard bool) []Hint {
	var hints []Hint
	if !a.Title {
		hints = append(hints, Hint{HintTitle, `Add a title: <title> in HTML, or frontmatter "title" / a first "# " heading in Markdown. Link previews lead with it.`})
	}
	if !a.Description {
		hints = append(hints, Hint{HintDescription, `Add a one- or two-sentence summary that states the takeaway: <meta name="description" content="..."> in HTML, or frontmatter "description" in Markdown. Without it, link previews fall back to the first paragraph.`})
	}
	if a.Stats == 0 {
		hints = append(hints, Hint{HintStats, `If it has key numbers, add up to six <meta name="trove:stat" content="Label: Value"> tags (HTML) or frontmatter "stats" (Markdown); link previews show them as a grid.`})
	}
	if !hasCard {
		hints = append(hints, Hint{HintCard, "For reports, proposals and dashboards, attach a preview card: a 1200x630 PNG with the title and one key visual, uploaded with PUT /api/artifacts/{slug}/preview. Link previews show it instead of an excerpt."})
	}
	return hints
}
