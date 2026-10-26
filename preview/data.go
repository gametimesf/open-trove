package preview

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register decoders for image.DecodeConfig
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"
)

// CSV excerpt limits.
const (
	csvPreviewRows = 4 // header plus three rows
	csvPreviewCols = 5
	csvCellWidth   = 16
	jsonPreviewKey = 8
)

// summarizeCSV counts rows and columns and renders the first rows as an
// aligned table. A truncated file reports a lower bound on rows.
func summarizeCSV(doc []byte, truncated bool) Summary {
	if truncated {
		// Drop the partial last line so it isn't counted or shown.
		if i := bytes.LastIndexByte(doc, '\n'); i >= 0 {
			doc = doc[:i]
		}
	}
	r := csv.NewReader(bytes.NewReader(doc))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var (
		s       Summary
		head    [][]string
		rows    int
		columns int
	)
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			continue
		}
		rows++
		columns = max(columns, len(rec))
		if len(head) < csvPreviewRows {
			head = append(head, rec)
		}
	}
	if rows == 0 {
		return s
	}
	dataRows := plural(rows-1, "row", "rows")
	if truncated {
		dataRows = formatInt(rows-1) + "+ rows"
	}
	s.Facts = append(s.Facts, dataRows+" × "+plural(columns, "column", "columns"))
	s.Snippet = csvTable(head, columns)
	return s
}

// csvTable aligns up to csvPreviewCols columns into fixed-width cells.
func csvTable(rows [][]string, columns int) string {
	shown := min(columns, csvPreviewCols)
	widths := make([]int, shown)
	for _, row := range rows {
		for i := 0; i < shown && i < len(row); i++ {
			widths[i] = max(widths[i], len([]rune(clip(row[i], csvCellWidth))))
		}
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		cells := make([]string, 0, shown+1)
		for i := range shown {
			cell := ""
			if i < len(row) {
				cell = clip(row[i], csvCellWidth)
			}
			cells = append(cells, cell+strings.Repeat(" ", widths[i]-len([]rune(cell))))
		}
		if columns > shown {
			cells = append(cells, "…")
		}
		lines = append(lines, clipLine(strings.Join(cells, "  "), maxSnippetCols))
	}
	return strings.Join(lines, "\n")
}

// summarizeJSON describes the top-level shape: an object's keys or an
// array's length. Truncated or invalid JSON falls back to a text excerpt.
func summarizeJSON(doc []byte, truncated bool) Summary {
	var top any
	if truncated || json.Unmarshal(doc, &top) != nil {
		return summarizeText(doc, truncated)
	}
	var s Summary
	switch v := top.(type) {
	case map[string]any:
		s.Facts = append(s.Facts, "object with "+plural(len(v), "key", "keys"))
		keys := orderedKeys(doc, jsonPreviewKey)
		if len(keys) > 0 {
			more := ""
			if len(v) > len(keys) {
				more = ", …"
			}
			s.Snippet = "{ " + strings.Join(keys, ", ") + more + " }"
		}
	case []any:
		s.Facts = append(s.Facts, "array of "+plural(len(v), "item", "items"))
	}
	return s
}

// orderedKeys returns up to n top-level object keys in document order.
func orderedKeys(doc []byte, n int) []string {
	dec := json.NewDecoder(bytes.NewReader(doc))
	if _, err := dec.Token(); err != nil {
		return nil
	}
	keys := make([]string, 0, n)
	for dec.More() && len(keys) < n {
		tok, err := dec.Token()
		if err != nil {
			return keys
		}
		key, _ := tok.(string)
		keys = append(keys, fmt.Sprintf("%q", clip(key, csvCellWidth)))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}

// summarizeText shows the first non-blank lines.
func summarizeText(doc []byte, truncated bool) Summary {
	var s Summary
	text := strings.TrimLeft(strings.ToValidUTF8(string(doc), "�"), "\r\n")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if !truncated {
		total := len(lines)
		if strings.HasSuffix(text, "\n") {
			total--
		}
		if total > 0 {
			s.Facts = append(s.Facts, plural(total, "line", "lines"))
		}
	}
	excerpt := make([]string, 0, maxSnippetRows)
	for _, line := range lines {
		if len(excerpt) == maxSnippetRows {
			break
		}
		excerpt = append(excerpt, clipLine(line, maxSnippetCols))
	}
	s.Snippet = strings.TrimRight(strings.Join(excerpt, "\n"), "\n ")
	return s
}

// summarizeImage reports pixel dimensions for formats the standard library
// can decode.
func summarizeImage(doc []byte) Summary {
	var s Summary
	cfg, format, err := image.DecodeConfig(bytes.NewReader(doc))
	if err == nil {
		s.Facts = append(s.Facts, fmt.Sprintf("%d×%d %s", cfg.Width, cfg.Height, strings.ToUpper(format)))
	}
	return s
}
