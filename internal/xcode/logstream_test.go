package xcode

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseLogLineFixture(t *testing.T) {
	var got []LogEntry
	skipped := 0
	for _, l := range bytes.Split(readFixture(t, "log/stream.ndjson"), []byte("\n")) {
		if e, ok := ParseLogLine(l); ok {
			got = append(got, e)
		} else if len(l) > 0 {
			skipped++
		}
	}
	// The "Filtering the log data" header and an activityCreateEvent.
	if skipped != 2 {
		t.Errorf("skipped %d lines, want 2", skipped)
	}
	var levels []string
	for _, e := range got {
		levels = append(levels, e.Level)
	}
	if want := []string{"debug", "info", "default", "default", "error", "fault"}; !slices.Equal(levels, want) {
		t.Fatalf("levels = %q, want %q", levels, want)
	}

	e := got[4]
	want := LogEntry{
		Level:     LevelError,
		Subsystem: "com.apple.ManagedAppDistribution",
		Category:  "Daemon",
		Process:   "managedappdistributiond",
		Message:   "Simulator is not supported",
	}
	if e.Level != want.Level || e.Subsystem != want.Subsystem || e.Category != want.Category ||
		e.Process != want.Process || e.Message != want.Message || e.PID == 0 {
		t.Errorf("entry:\n got %+v\nwant %+v (with a PID)", e, want)
	}
	if e.Time.IsZero() || e.Time.Year() != 2026 {
		t.Errorf("time = %v, want a parsed 2026 timestamp", e.Time)
	}
	if !strings.Contains(got[3].Message, "\n") {
		t.Errorf("multi-line message lost its newline: %q", got[3].Message)
	}
}

func TestParseLogLineRejects(t *testing.T) {
	for _, l := range []string{
		"",
		"Filtering the log data using \"x\"",
		"{not json",
		`{"eventType": "activityCreateEvent", "eventMessage": "x"}`,
		`{"eventType": "signpostEvent", "eventMessage": "x"}`,
		`{"count":30,"finished":1}`, // printed by log stream when interrupted
	} {
		if e, ok := ParseLogLine([]byte(l)); ok {
			t.Errorf("ParseLogLine(%q) = %+v, want not ok", l, e)
		}
	}
	e, ok := ParseLogLine([]byte(`  {"eventType":"logEvent","messageType":"Fault","timestamp":"2026-11-08 10:00:00.123456+0100",` +
		`"eventMessage":"boom","subsystem":"s","category":"c","processImagePath":"/a/b/My App","processID":42}`))
	if !ok || e.Level != "fault" || e.Process != "My App" || e.PID != 42 || e.Time.Nanosecond() != 123456000 {
		t.Errorf("got %+v, %v", e, ok)
	}
}

func TestLogPredicate(t *testing.T) {
	for _, tc := range []struct {
		f      LogFilter
		marker string
		want   string
	}{
		{LogFilter{}, "", ""},
		{LogFilter{}, "runway-1", `eventMessage != "runway-1"`},
		{LogFilter{Subsystem: "com.example.app"}, "", `subsystem == "com.example.app"`},
		{LogFilter{Subsystem: "s", Category: "c", Process: "My App"}, "m",
			`subsystem == "s" AND category == "c" AND process == "My App" AND eventMessage != "m"`},
		{LogFilter{Subsystem: `a"b\c`}, "", `subsystem == "a\"b\\c"`},
		{LogFilter{Subsystem: "ignored", Predicate: `messageType == error OR subsystem == "x"`}, "m",
			`(messageType == error OR subsystem == "x") AND eventMessage != "m"`},
	} {
		if got := tc.f.predicate(tc.marker); got != tc.want {
			t.Errorf("%+v / %q:\n got %s\nwant %s", tc.f, tc.marker, got, tc.want)
		}
	}
}

func TestLogShowArgs(t *testing.T) {
	for _, tc := range []struct {
		level string
		want  []string
	}{
		{"", []string{"show", "--style", "ndjson", "--info", "--debug"}},
		{LevelInfo, []string{"show", "--style", "ndjson", "--info"}},
		{LevelDefault, []string{"show", "--style", "ndjson"}},
	} {
		if got := (LogFilter{Level: tc.level}).showArgs(); !slices.Equal(got, tc.want) {
			t.Errorf("level %q: got %q, want %q", tc.level, got, tc.want)
		}
	}
}

// fastLogStop shortens LogStream.Stop's timing for a test.
func fastLogStop(t *testing.T) {
	t.Helper()
	poll, attempts, grace, marker := logPIDPoll, logPIDAttempts, logStopGrace, newLogMarker
	logPIDPoll, logPIDAttempts, logStopGrace = time.Millisecond, 5, 50*time.Millisecond
	newLogMarker = func() string { return "runway-test" }
	t.Cleanup(func() { logPIDPoll, logPIDAttempts, logStopGrace, newLogMarker = poll, attempts, grace, marker })
}

func TestSimLogStreamArgsAndStop(t *testing.T) {
	fastLogStop(t)
	f := &FakeRunner{}
	f.Respond(FakeResult{
		Block:  true,
		Stdout: []byte("{\"eventType\":\"logEvent\"}\n"),
		Stderr: []byte("getpwuid_r did not find a match for uid 501\nreal problem\n"),
	}, "xcrun", "simctl", "spawn", "UDID", "log", "stream", "--style", "ndjson", "--level", "info",
		"--predicate", `subsystem == "com.example.app" AND eventMessage != "runway-test"`)
	f.Respond(FakeResult{Stdout: []byte("4242\n")}, "pgrep", "-f", "^log stream .*runway-test")

	var out, errOut bytes.Buffer
	s, err := New(f).SimLogStream(context.Background(), "UDID", LogFilter{Level: LevelInfo, Subsystem: "com.example.app"},
		LogOutput{Stdout: &out, Stderr: &errOut})
	if err != nil {
		t.Fatal(err)
	}
	s.Stop(context.Background())
	<-s.Done()

	if got := callStrings(f); !slices.Equal(got[1:], []string{"pgrep -f '^log stream .*runway-test'", "kill -INT 4242"}) {
		t.Errorf("stop commands = %q", got[1:])
	}
	if out.String() != "{\"eventType\":\"logEvent\"}\n" {
		t.Errorf("stdout = %q", out.String())
	}
	if errOut.String() != "real problem\n" {
		t.Errorf("stderr = %q, want only the real problem", errOut.String())
	}
}

func TestSimLogStreamStopRetriesUntilFound(t *testing.T) {
	fastLogStop(t)
	f := &FakeRunner{}
	f.Respond(FakeResult{Block: true}, "xcrun", "simctl", "spawn", "UDID", "log", "stream", "--style", "ndjson", "--level", "debug",
		"--predicate", `eventMessage != "runway-test"`)
	pgreps := 0
	f.Handler = func(c Cmd) (FakeResult, bool) {
		if c.Name != "pgrep" {
			return FakeResult{}, false
		}
		pgreps++
		if pgreps < 3 { // log hasn't started yet
			return FakeResult{Err: &ExitError{Cmd: c, Code: 1}}, true
		}
		return FakeResult{Stdout: []byte("7\n8\n")}, true
	}
	s, err := New(f).SimLogStream(context.Background(), "UDID", LogFilter{}, LogOutput{})
	if err != nil {
		t.Fatal(err)
	}
	s.Stop(context.Background())
	if pgreps != 3 {
		t.Errorf("pgrep ran %d times, want 3", pgreps)
	}
	got := callStrings(f)
	if want := []string{"kill -INT 7", "kill -INT 8"}; !slices.Equal(got[len(got)-2:], want) {
		t.Errorf("commands = %q, want to end with %q", got, want)
	}
}

func TestSimLogStreamStopWhenLogNeverStarts(t *testing.T) {
	fastLogStop(t)
	f := &FakeRunner{Default: FakeResult{Err: &ExitError{Code: 1}}} // pgrep finds nothing
	f.Respond(FakeResult{Block: true}, "xcrun", "simctl", "spawn", "UDID", "log", "stream", "--style", "ndjson", "--level", "debug",
		"--predicate", `eventMessage != "runway-test"`)
	s, err := New(f).SimLogStream(context.Background(), "UDID", LogFilter{}, LogOutput{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.Stop(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop hung")
	}
	select {
	case <-s.Done():
	default:
		t.Error("simctl was not stopped")
	}
	for _, c := range callStrings(f) {
		if strings.HasPrefix(c, "kill") {
			t.Errorf("killed something: %s", c)
		}
	}
}

func TestSimLogStreamIgnoresParentCancel(t *testing.T) {
	fastLogStop(t)
	f := &FakeRunner{Default: FakeResult{Err: &ExitError{Code: 1}}}
	f.Respond(FakeResult{Block: true}, "xcrun", "simctl", "spawn", "UDID", "log", "stream", "--style", "ndjson", "--level", "debug",
		"--predicate", `eventMessage != "runway-test"`)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := New(f).SimLogStream(ctx, "UDID", LogFilter{}, LogOutput{})
	if err != nil {
		t.Fatal(err)
	}
	cancel() // Ctrl-C: only Stop may end the stream
	select {
	case <-s.Done():
		t.Fatal("stream ended on Ctrl-C without Stop")
	case <-time.After(20 * time.Millisecond):
	}
	s.Stop(context.Background())
}

func TestSimLogShowArgs(t *testing.T) {
	f := &FakeRunner{}
	err := New(f).SimLogShow(context.Background(), "UDID", "5m", LogFilter{Level: LevelInfo, Subsystem: "s"}, LogOutput{})
	if err != nil {
		t.Fatal(err)
	}
	want := `xcrun simctl spawn UDID log show --style ndjson --info --predicate 'subsystem == "s"' --last 5m`
	if got := callStrings(f); !slices.Equal(got, []string{want}) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestDeviceLogArgs(t *testing.T) {
	f := &FakeRunner{}
	c := New(f)
	if err := c.DeviceLogCollect(context.Background(), "00008103-X", "5m", "/tmp/a b/r.logarchive"); err != nil {
		t.Fatal(err)
	}
	if err := c.LogShowArchive(context.Background(), "/tmp/a b/r.logarchive", LogFilter{Subsystem: "s"}, LogOutput{}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"log collect --device-udid 00008103-X --last 5m --output '/tmp/a b/r.logarchive'",
		`log show --style ndjson --info --debug --predicate 'subsystem == "s"' '/tmp/a b/r.logarchive'`,
	}
	if got := callStrings(f); !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestLogNoise(t *testing.T) {
	var b bytes.Buffer
	w := logNoise(&b)
	_, _ = w.Write([]byte("getpwuid_r did not find a match for uid 501\nFiltering the log data using \"x\"\nlog: bad predicate\npartial"))
	w.Flush()
	if b.String() != "log: bad predicate\npartial\n" {
		t.Errorf("got %q", b.String())
	}
}

func callStrings(f *FakeRunner) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.String())
	}
	return out
}

func TestSimLogStreamWaitStarted(t *testing.T) {
	fastLogStop(t)
	f := &FakeRunner{}
	f.Respond(FakeResult{Block: true}, "xcrun", "simctl", "spawn", "UDID", "log", "stream", "--style", "ndjson", "--level", "debug",
		"--predicate", `eventMessage != "runway-test"`)
	pgreps := 0
	f.Handler = func(c Cmd) (FakeResult, bool) {
		if c.Name != "pgrep" {
			return FakeResult{}, false
		}
		pgreps++
		if pgreps < 2 {
			return FakeResult{Err: &ExitError{Cmd: c, Code: 1}}, true
		}
		return FakeResult{Stdout: []byte("9\n")}, true
	}
	s, err := New(f).SimLogStream(context.Background(), "UDID", LogFilter{}, LogOutput{})
	if err != nil {
		t.Fatal(err)
	}
	s.WaitStarted(context.Background())
	s.Stop(context.Background())
	if pgreps != 2 {
		t.Errorf("pgrep ran %d times, want 2 (Stop reuses the PID WaitStarted found)", pgreps)
	}
	if got := callStrings(f); got[len(got)-1] != "kill -INT 9" {
		t.Errorf("commands = %q", got)
	}
}

// actortest.ndjson is `runway logs --json` for testdata's sample app, which
// logs at every level once a second, interrupted with Ctrl-C.
func TestParseLogLineActorTest(t *testing.T) {
	levels := map[string]int{}
	var n, skipped int
	for _, l := range bytes.Split(bytes.TrimSpace(readFixture(t, "log/actortest.ndjson")), []byte("\n")) {
		e, ok := ParseLogLine(l)
		if !ok {
			skipped++
			continue
		}
		n++
		levels[e.Level]++
		if e.Subsystem != "com.example.ActorTest" || e.Process != "ActorTest" || e.PID == 0 || e.Time.IsZero() {
			t.Errorf("entry %+v", e)
		}
		if want := e.Level + " tick "; e.Category == "tick" && e.Level != LevelDefault && !strings.HasPrefix(e.Message, want) {
			t.Errorf("message %q, want %q…", e.Message, want)
		}
	}
	if n != 30 || skipped != 1 { // the final {"count":30,"finished":1}
		t.Errorf("parsed %d, skipped %d; want 30 and 1", n, skipped)
	}
	for _, l := range []string{LevelDebug, LevelInfo, LevelDefault, LevelError, LevelFault} {
		if levels[l] == 0 {
			t.Errorf("no %s messages (levels: %v)", l, levels)
		}
	}
}
