package ui

import (
	"io"
	"strings"
	"unicode/utf8"
)

// Row is one table row. Style, if set, is applied to the whole padded line,
// so colour codes never disturb column alignment.
type Row struct {
	Cells []string
	Style func(string) string
}

// Table is a simple left-aligned table with two spaces between columns.
type Table struct {
	Header      []string
	HeaderStyle func(string) string
	Rows        []Row
}

// Render writes the table to w.
func (t Table) Render(w io.Writer) error {
	widths := make([]int, len(t.Header))
	measure := func(cells []string) {
		for i, c := range cells {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], utf8.RuneCountInString(c))
		}
	}
	measure(t.Header)
	for _, r := range t.Rows {
		measure(r.Cells)
	}

	var b strings.Builder
	line := func(cells []string, style func(string) string) {
		var l strings.Builder
		for i, c := range cells {
			l.WriteString(c)
			if i < len(cells)-1 {
				l.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c)+2))
			}
		}
		s := l.String()
		if style != nil {
			s = style(s)
		}
		b.WriteString(s)
		b.WriteByte('\n')
	}
	if len(t.Header) > 0 {
		line(t.Header, t.HeaderStyle)
	}
	for _, r := range t.Rows {
		line(r.Cells, r.Style)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
