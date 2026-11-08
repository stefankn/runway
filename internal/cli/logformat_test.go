package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

func TestLogFormat(t *testing.T) {
	at := time.Date(2026, 11, 8, 9, 47, 3, 458_139_000, time.FixedZone("", 3600))
	for _, tc := range []struct {
		name string
		p    logPrinter
		e    xcode.LogEntry
		want string
	}{
		{"plain", logPrinter{}, xcode.LogEntry{Time: at, Level: "info", Category: "net", Process: "App", Message: "hi"},
			"09:47:03.458 info    [net] hi\n"},
		{"no category", logPrinter{}, xcode.LogEntry{Time: at, Level: "default", Message: "hi"},
			"09:47:03.458 default hi\n"},
		{"process", logPrinter{showProcess: true}, xcode.LogEntry{Time: at, Level: "fault", Category: "c", Process: "My App", Message: "x"},
			"09:47:03.458 fault   My App [c] x\n"},
		{"multi-line", logPrinter{prefix: "log │ "}, xcode.LogEntry{Time: at, Level: "error", Category: "c", Message: "one\ntwo\n"},
			"log │ 09:47:03.458 error   [c] one\nlog │                          two\n"},
	} {
		if got := tc.p.format(tc.e); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestLogFormatColours(t *testing.T) {
	p := logPrinter{st: ui.Styler{Enabled: true}}
	at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	got := p.format(xcode.LogEntry{Time: at, Level: "fault", Message: "boom"})
	if want := "\x1b[2m09:00:00.000\x1b[0m \x1b[31mfault  \x1b[0m boom\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLogPrinterWriter(t *testing.T) {
	var b bytes.Buffer
	w := (&logPrinter{w: &b}).writer()
	line := `{"eventType":"logEvent","messageType":"Info","timestamp":"2026-11-08 09:00:00.000001+0000","eventMessage":"hi","category":"c"}`
	_, _ = w.Write([]byte("Filtering the log data\n" + line[:20]))
	_, _ = w.Write([]byte(line[20:])) // no newline yet
	if b.Len() != 0 {
		t.Fatalf("printed a partial line: %q", b.String())
	}
	w.Flush()
	if want := "09:00:00.000 info    [c] hi\n"; b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}
