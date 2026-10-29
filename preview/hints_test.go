package preview

import (
	"reflect"
	"testing"
)

func codes(hints []Hint) []string {
	out := []string{}
	for _, h := range hints {
		out = append(out, h.Code)
	}
	return out
}

func TestHints(t *testing.T) {
	tests := []struct {
		name, contentType, filename, doc string
		hasCard                          bool
		want                             []string
	}{
		{
			name: "fully annotated HTML with a card", contentType: "text/html", filename: "r.html", hasCard: true,
			doc:  `<title>Q3</title><meta name="description" content="Beat plan."><meta name="trove:stat" content="GMV: $1M">`,
			want: []string{},
		},
		{
			name: "bare HTML gets every hint", contentType: "text/html", filename: "r.html",
			doc:  `<h1>Q3</h1><p>Some paragraph that is long enough to be a description.</p>`,
			want: []string{HintTitle, HintDescription, HintStats, HintCard},
		},
		{
			name: "fallback description isn't authored", contentType: "text/html", filename: "r.html", hasCard: true,
			doc:  `<title>Q3</title><p>A first paragraph long enough to become the preview summary.</p>`,
			want: []string{HintDescription, HintStats},
		},
		{
			name: "markdown with frontmatter", contentType: "text/markdown", filename: "d.md",
			doc:  "---\ndescription: Why p99 doubled.\nstats:\n  - \"p99: 3s\"\n---\n# Checkout\n",
			want: []string{HintCard},
		},
		{
			name: "markdown heading counts as a title", contentType: "text/markdown", filename: "d.md", hasCard: true,
			doc:  "# Plan\n\nText.\n",
			want: []string{HintDescription, HintStats},
		},
		{
			name: "slides without #id handling", contentType: "text/html", filename: "deck.html", hasCard: true,
			doc:  `<title>Deck</title><meta name="description" content="x"><meta name="trove:stat" content="A: 1"><div class="slide active">1</div><div class="slide">2</div>`,
			want: []string{HintNavigation},
		},
		{
			name: "slides that handle #id", contentType: "text/html", filename: "deck.html", hasCard: true,
			doc:  `<title>Deck</title><meta name="description" content="x"><meta name="trove:stat" content="A: 1"><div class="slide">1</div><div class="slide">2</div><script>addEventListener("hashchange", go)</script>`,
			want: []string{},
		},
		{
			name: "tab panels without #id handling", contentType: "text/html", filename: "tabs.html", hasCard: true,
			doc:  `<title>T</title><meta name="description" content="x"><meta name="trove:stat" content="A: 1"><div role="tabpanel">a</div>`,
			want: []string{HintNavigation},
		},
		{name: "CSV gets no hints", contentType: "text/csv", filename: "x.csv", doc: "a,b\n1,2\n"},
		{name: "images get no hints", contentType: "image/png", filename: "x.png", doc: "\x89PNG"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Hints(tt.contentType, tt.filename, []byte(tt.doc), tt.hasCard)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("hints = %v, want none", codes(got))
				}
				return
			}
			if !reflect.DeepEqual(codes(got), tt.want) {
				t.Fatalf("hints = %v, want %v", codes(got), tt.want)
			}
		})
	}
	if got := codes(SiteHints([]byte(`<title>Docs</title>`), false)); !reflect.DeepEqual(got, []string{HintDescription, HintStats, HintCard}) {
		t.Fatalf("site hints = %v", got)
	}
}
