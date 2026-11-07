package cli

import (
	"io"
	"slices"
	"strings"
	"testing"
)

func TestHintScanner(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   []int // indexes into buildHints
	}{
		{"error: Signing for \"App\" requires a development team.\n", []int{0}},
		{"error: No Account for Team \"ABC\".\n", []int{0}},
		{"Unable to launch: the device is locked.\n", []int{1}},
		{"error: Developer Mode disabled\n", []int{2}},
		{"error: The device is not paired with this Mac\n", []int{3}},
		{"xcodebuild: error: Unable to find a device matching the provided destination specifier:\n", []int{4}},
		{"** BUILD SUCCEEDED **\n", nil},
		{"requires a development team\nUnable to find a device matching\n", []int{0, 4}},
		{"no trailing newline: device is locked", []int{1}},
	} {
		h := newHintScanner()
		_, _ = io.WriteString(h, tc.output)
		var want []string
		for _, i := range tc.want {
			want = append(want, buildHints[i].text)
		}
		if got := h.Hints(); !slices.Equal(got, want) {
			t.Errorf("%q:\n got %q\nwant %q", tc.output, got, want)
		}
	}
}

func TestHintScannerSplitWrites(t *testing.T) {
	h := newHintScanner()
	_, _ = io.WriteString(h, "error: Signing requires a deve")
	_, _ = io.WriteString(h, "lopment team.\nnext line\n")
	if got := h.Hints(); len(got) != 1 || !strings.Contains(got[0], "--team") {
		t.Errorf("hints = %q", got)
	}
}

func TestHintScannerBoundsLongLines(t *testing.T) {
	h := newHintScanner()
	_, _ = io.WriteString(h, strings.Repeat("x", 3*maxLine))
	if len(h.partial) > maxLine {
		t.Errorf("kept %d bytes of an unfinished line", len(h.partial))
	}
}
