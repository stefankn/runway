package ui

import (
	"bytes"
	"os"
	"testing"
)

func TestTableRender(t *testing.T) {
	st := Styler{Enabled: true}
	var b bytes.Buffer
	err := Table{
		Header: []string{"NAME", "OS", "UDID"},
		Rows: []Row{
			{Cells: []string{"Test Watch", "-", "A1"}},
			{Cells: []string{"iPad", "27.2", "B2"}, Style: st.Green},
		},
	}.Render(&b)
	if err != nil {
		t.Fatal(err)
	}
	want := "NAME        OS    UDID\n" +
		"Test Watch  -     A1\n" +
		"\x1b[32miPad        27.2  B2\x1b[0m\n"
	if b.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", b.String(), want)
	}
}

func TestTableNoHeader(t *testing.T) {
	var b bytes.Buffer
	if err := (Table{Rows: []Row{{Cells: []string{"a", "bb"}}, {Cells: []string{"ccc", "d"}}}}).Render(&b); err != nil {
		t.Fatal(err)
	}
	if want := "a    bb\nccc  d\n"; b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestStylerDisabled(t *testing.T) {
	st := Styler{}
	if got := st.Green("x") + st.Bold("y") + st.Dim("z"); got != "xyz" {
		t.Errorf("disabled styler changed text: %q", got)
	}
	if got := (Styler{Enabled: true}).Bold(""); got != "" {
		t.Errorf("empty text styled: %q", got)
	}
}

func TestColorEnabled(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	for _, tc := range []struct {
		name  string
		tty   bool
		vars  map[string]string
		color bool
	}{
		{"tty", true, map[string]string{"TERM": "xterm-256color"}, true},
		{"not a tty", false, map[string]string{"TERM": "xterm-256color"}, false},
		{"NO_COLOR", true, map[string]string{"NO_COLOR": "1"}, false},
		{"dumb terminal", true, map[string]string{"TERM": "dumb"}, false},
	} {
		if got := colorEnabled(tc.tty, env(tc.vars)); got != tc.color {
			t.Errorf("%s: colorEnabled = %v, want %v", tc.name, got, tc.color)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	if IsTerminal(&bytes.Buffer{}) {
		t.Error("buffer reported as terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) {
		t.Error("regular file reported as terminal")
	}
}
