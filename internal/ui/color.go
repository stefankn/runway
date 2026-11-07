// Package ui holds terminal presentation helpers: colours and tables.
package ui

import (
	"io"
	"os"
)

// Styler applies ANSI styles when Enabled, and returns text unchanged otherwise.
type Styler struct {
	Enabled bool
}

// NewStyler enables colour only when w is a terminal, NO_COLOR is unset
// (https://no-color.org) and TERM is not "dumb".
func NewStyler(w io.Writer) Styler {
	return Styler{Enabled: colorEnabled(IsTerminal(w), os.Getenv)}
}

func colorEnabled(isTTY bool, getenv func(string) string) bool {
	return isTTY && getenv("NO_COLOR") == "" && getenv("TERM") != "dumb"
}

// IsTerminal reports whether w is a character device such as a terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (s Styler) wrap(code, text string) string {
	if !s.Enabled || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// Bold renders text in bold.
func (s Styler) Bold(text string) string { return s.wrap("1", text) }

// Dim renders text faint.
func (s Styler) Dim(text string) string { return s.wrap("2", text) }

// Green renders text green.
func (s Styler) Green(text string) string { return s.wrap("32", text) }
