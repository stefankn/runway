package cli

import (
	"encoding/json"
	"io"
)

// writeJSON writes v as indented JSON followed by a newline.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// orDash returns s, or "-" when s is empty, for table cells.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
