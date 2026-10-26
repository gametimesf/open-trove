package preview

import (
	"bytes"
	"image"
	"image/png"
	"reflect"
	"strings"
	"testing"
)

const reportHTML = `<!doctype html>
<html><head>
<title>Q3 Revenue Review</title>
<meta name="description" content="How Q3 landed against plan,   and what we change for Q4.">
<meta name="trove:stat" content="Revenue: $1.2M">
<meta name="trove:stat" content="Orders: 4,210">
<meta name="trove:stat" content="no label here">
<style>h1 { color: red }</style>
</head><body>
<nav><p>Home · Reports · A very long navigation paragraph that should be ignored entirely</p></nav>
<h1>Ignored because title wins</h1>
<script>document.write("<h2>not a section</h2>")</script>
<h2>Summary</h2><p>Short.</p>
<h2>Drivers <small>(top 3)</small></h2>
<h2>Next steps</h2>
</body></html>`

func TestSummarizeHTML(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want Summary
	}{
		{
			name: "meta tags, title and h2 sections",
			doc:  reportHTML,
			want: Summary{
				Kind: KindHTML, Title: "Q3 Revenue Review", Description: "How Q3 landed against plan, and what we change for Q4.",
				Sections: []string{"Summary", "Drivers (top 3)", "Next steps"},
				Stats:    []Stat{{Label: "Revenue", Value: "$1.2M"}, {Label: "Orders", Value: "4,210"}},
			},
		},
		{
			name: "og tags win over title and description",
			doc:  `<title>Plain</title><meta property="og:title" content="Rich title"><meta property="og:description" content="Rich description">`,
			want: Summary{Kind: KindHTML, Title: "Rich title", Description: "Rich description"},
		},
		{
			name: "falls back to h1 and first substantial paragraph outside page chrome",
			doc: `<header><p>Gametime internal tools — a header paragraph long enough to count</p></header>
				<h1>Launch <em>plan</em></h1><p>Too short.</p>
				<p>The launch moves to November so pricing and inventory can ship together.</p>`,
			want: Summary{Kind: KindHTML, Title: "Launch plan", Description: "The launch moves to November so pricing and inventory can ship together."},
		},
		{
			name: "fragment without structure",
			doc:  `<div>hello</div>`,
			want: Summary{Kind: KindHTML},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Summarize("text/html; charset=utf-8", "report.html", []byte(tt.doc))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestSummarizeMarkdown(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want Summary
	}{
		{
			name: "frontmatter, headings, paragraph and diagrams",
			doc: "---\ntitle: Checkout latency\ndescription: Why p99 doubled on Friday.\nstats:\n  - \"p99: 1.8s\"\n  - \"Error rate: 0.4%\"\n---\n" +
				"# Ignored H1\n\nIntro paragraph.\n\n## Timeline\n\n```mermaid\ngraph TD\n## not a heading\n```\n\n## Root cause\n",
			want: Summary{
				Kind: KindMarkdown, Title: "Checkout latency", Description: "Why p99 doubled on Friday.",
				Sections: []string{"Timeline", "Root cause"},
				Stats:    []Stat{{Label: "p99", Value: "1.8s"}, {Label: "Error rate", Value: "0.4%"}},
				Facts:    []string{"1 min read", "1 diagram"},
			},
		},
		{
			name: "first heading and first prose paragraph with inline markup removed",
			doc:  "# Pricing **v2** rollout\n\n- a list item first\n\nWe ship [dynamic fees](https://x.test/a) behind\na `flag` in *two* stages.\n\nSecond paragraph.\n",
			want: Summary{
				Kind: KindMarkdown, Title: "Pricing v2 rollout",
				Description: "We ship dynamic fees behind a flag in two stages.",
				Facts:       []string{"1 min read"},
			},
		},
		{
			name: "malformed frontmatter is dropped",
			doc:  "---\ntitle: [unclosed\n---\nBody text after the block.\n",
			want: Summary{Kind: KindMarkdown, Description: "Body text after the block.", Facts: []string{"1 min read"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Summarize("text/markdown; charset=utf-8", "doc.md", []byte(tt.doc))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestSummarizeData(t *testing.T) {
	pngBytes := func() []byte {
		var buf bytes.Buffer
		if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 640, 480))); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	tests := []struct {
		name, contentType, filename string
		doc                         []byte
		want                        Summary
	}{
		{
			name: "csv table and counts", contentType: "text/csv", filename: "sales.csv",
			doc: []byte("region,orders,revenue,margin,owner,notes\nwest,120,9000,0.2,ana,ok\neast,80,6100,0.1,bo,\"quoted, cell\"\nsouth,5,300,0.3,cy,x\nnorth,1,1,1,d,y\n"),
			want: Summary{
				Kind: KindCSV, Facts: []string{"4 rows × 6 columns"},
				Snippet: "region  orders  revenue  margin  owner  …\nwest    120     9000     0.2     ana    …\neast    80      6100     0.1     bo     …\nsouth   5       300      0.3     cy     …",
			},
		},
		{
			name: "json object keys in order", contentType: "application/json", filename: "c.json",
			doc:  []byte(`{"zeta": 1, "alpha": {"nested": true}, "mid": [1,2]}`),
			want: Summary{Kind: KindJSON, Facts: []string{"object with 3 keys"}, Snippet: `{ "zeta", "alpha", "mid" }`},
		},
		{
			name: "json array", contentType: "application/json", filename: "a.json",
			doc:  []byte(`[1,2,3]`),
			want: Summary{Kind: KindJSON, Facts: []string{"array of 3 items"}},
		},
		{
			name: "code shows language, line count and first lines", contentType: "text/plain", filename: "main.go",
			doc:  []byte("package main\n\nfunc main() {}\n"),
			want: Summary{Kind: KindCode, Facts: []string{"GO", "3 lines"}, Snippet: "package main\n\nfunc main() {}"},
		},
		{
			name: "image dimensions", contentType: "image/png", filename: "chart.png",
			doc:  pngBytes(),
			want: Summary{Kind: KindImage, Facts: []string{"640×480 PNG"}},
		},
		{
			name: "pdf has no content summary", contentType: "application/pdf", filename: "deck.pdf",
			doc:  []byte("%PDF-1.7"),
			want: Summary{Kind: KindPDF},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Summarize(tt.contentType, tt.filename, tt.doc)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestSummarizeTruncated(t *testing.T) {
	var b strings.Builder
	b.WriteString("id,name\n")
	for b.Len() <= MaxBytes {
		b.WriteString("12345,some longer name value\n")
	}
	got := Summarize("text/csv", "big.csv", []byte(b.String()))
	if len(got.Facts) != 1 || !strings.Contains(got.Facts[0], "+ rows × 2 columns") {
		t.Fatalf("facts = %q, want a lower-bound row count", got.Facts)
	}

	long := []byte(`{"a": "` + strings.Repeat("x", MaxBytes) + `"}`)
	if got := Summarize("application/json", "big.json", long); got.Facts != nil || got.Snippet == "" {
		t.Fatalf("truncated json = %#v, want text excerpt without facts", got)
	}
}

func TestSummarizeSite(t *testing.T) {
	got := SummarizeSite([]byte(`<title>Docs</title><h2>Install</h2>`), 12)
	want := Summary{Kind: KindSite, Title: "Docs", Sections: []string{"Install"}, Facts: []string{"12 files"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestSummaryBounds(t *testing.T) {
	var b strings.Builder
	b.WriteString("<title>" + strings.Repeat("word ", 100) + "</title>")
	for range 10 {
		b.WriteString(`<meta name="trove:stat" content="A: 1"><h2>S</h2>`)
	}
	got := Summarize("text/html", "x.html", []byte(b.String()))
	if n := len([]rune(got.Title)); n > maxTitle || !strings.HasSuffix(got.Title, "…") {
		t.Errorf("title runes = %d (%q)", n, got.Title)
	}
	if len(got.Stats) != maxStats || len(got.Sections) != maxSections {
		t.Errorf("stats = %d sections = %d", len(got.Stats), len(got.Sections))
	}
}

func TestKindLabel(t *testing.T) {
	if KindMarkdown.Label() != "Markdown" || Kind("other").Label() != "File" {
		t.Fatal("unexpected labels")
	}
}
