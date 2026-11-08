package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stefankn/runway/internal/xcode"
)

const (
	bootedUDID = "00000000-0000-4000-8000-00000000000B" // iPad (A16), the only booted simulator in the fixture
	logPID     = "4242"
)

// withLogStream makes f answer the commands of a simulator log stream: the
// stream prints stdout and then runs until runway interrupts its `log`
// process (found by pgrep as logPID).
func withLogStream(t *testing.T, f *xcode.FakeRunner, stdout []byte) {
	t.Helper()
	inner := f.Handler
	f.Handler = func(c xcode.Cmd) (xcode.FakeResult, bool) {
		line := c.Name + " " + strings.Join(c.Args, " ")
		switch {
		case strings.HasPrefix(line, "xcrun simctl spawn") && strings.Contains(line, " log stream "):
			return xcode.FakeResult{Block: true, Stdout: stdout}, true
		case c.Name == "pgrep":
			return xcode.FakeResult{Stdout: []byte(logPID + "\n")}, true
		case c.Name == "kill":
			// The real `log` exits on SIGINT, and simctl with it. The
			// FakeRunner is locked while this handler runs, hence the goroutine.
			go func() {
				for _, p := range f.Processes() {
					if slices.Contains(p.Cmd.Args, "stream") {
						p.Exit(&xcode.ExitError{Cmd: p.Cmd, Code: 1})
					}
				}
			}()
			return xcode.FakeResult{}, true
		}
		if inner != nil {
			return inner(c)
		}
		return xcode.FakeResult{}, false
	}
}

// logsFake is a FakeRunner for `runway logs` in the folder made by runSetup.
func logsFake(t *testing.T) (dir string, f *xcode.FakeRunner) {
	t.Helper()
	dir, appPath := runSetup(t, true)
	f = runFake(t, appPath, xcode.FakeResult{}, xcode.FakeResult{})
	withLogStream(t, f, fixture(t, "log/stream.ndjson"))
	return dir, f
}

// runLogs runs `runway logs args…` and presses Ctrl-C once the stream has
// started. It returns runway's output and error.
func runLogs(t *testing.T, dir string, f *xcode.FakeRunner, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		out, errOut string
		err         error
	}
	done := make(chan result, 1)
	go func() {
		out, errOut, err := runOptsCtx(t, ctx, &options{workDir: dir, testRunner: f}, append([]string{"logs"}, args...)...)
		done <- result{out, errOut, err}
	}()
	deadline := time.After(5 * time.Second)
	for !hasCommand(f, "xcrun simctl spawn") {
		select {
		case r := <-done: // finished without streaming (an error, or --last)
			return r.out, r.errOut, r.err
		case <-deadline:
			t.Fatal("log stream never started")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case r := <-done:
		return r.out, r.errOut, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("logs did not return after Ctrl-C")
	}
	return "", "", nil
}

// streamPredicate returns the --predicate of the recorded log stream.
func streamPredicate(t *testing.T, f *xcode.FakeRunner) string {
	t.Helper()
	return argAfter(commandArgs(t, f, "xcrun simctl spawn"), "--predicate")
}

func TestLogsStreamsAppSubsystem(t *testing.T) {
	dir, f := logsFake(t)
	out, errOut, err := runLogs(t, dir, f)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled (exit 130)\n%s", err, errOut)
	}

	args := commandArgs(t, f, "xcrun simctl spawn")
	if want := []string{"simctl", "spawn", bootedUDID, "log", "stream", "--style", "ndjson", "--level", "debug", "--predicate"}; !slices.Equal(args[:len(want)], want) {
		t.Errorf("stream args = %q", args)
	}
	if p := streamPredicate(t, f); !strings.HasPrefix(p, `subsystem == "`+testBundleID+`" AND eventMessage != "runway-`) {
		t.Errorf("predicate = %s", p)
	}
	if got := commandArgs(t, f, "kill"); !slices.Equal(got, []string{"-INT", logPID}) {
		t.Errorf("kill args = %q, want the log process interrupted", got)
	}

	for _, want := range []string{
		"error   [Daemon] Simulator is not supported\n",
		"fault   [TabiUserEventHistoryScoring] Unable to build",
		"debug   [Position] #locctl-hb",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Filtering the log data") || strings.Contains(out, "Activity for state dumps") {
		t.Errorf("non-messages were printed:\n%s", out)
	}
	if !strings.Contains(errOut, "Streaming "+testBundleID+" on iPad (A16) (Ctrl-C to stop)") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestLogsSubsystemSources(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		args      []string
		want      string // predicate prefix
	}{
		{"config", "com.example.custom", nil, `subsystem == "com.example.custom" AND`},
		{"all", "", []string{"--all"}, `eventMessage != "runway-`},
		{"predicate", "", []string{"--predicate", `messageType == error`}, `(messageType == error) AND eventMessage`},
		{"category", "com.x", []string{"--category", "net"}, `subsystem == "com.x" AND category == "net" AND`},
	} {
		dir, f := logsFake(t)
		if tc.env != "" {
			t.Setenv("RUNWAY_LOG_SUBSYSTEM", tc.env)
		}
		_, errOut, err := runLogs(t, dir, f, tc.args...)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: err = %v\n%s", tc.name, err, errOut)
		}
		if p := streamPredicate(t, f); !strings.HasPrefix(p, tc.want) {
			t.Errorf("%s: predicate = %s, want %s…", tc.name, p, tc.want)
		}
		if hasCommand(f, "xcodebuild") {
			t.Errorf("%s: looked up the project although no bundle ID is needed: %q", tc.name, commandNames(f))
		}
	}
}

func TestLogsAllShowsProcess(t *testing.T) {
	dir, f := logsFake(t)
	out, _, _ := runLogs(t, dir, f, "--all")
	if !strings.Contains(out, "error   managedappdistributiond [Daemon] Simulator is not supported") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestLogsJSON(t *testing.T) {
	dir, f := logsFake(t)
	out, _, _ := runLogs(t, dir, f, "--json")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 7 { // every JSON line of the fixture, not the text header
		t.Fatalf("got %d lines, want 7:\n%s", len(lines), out)
	}
	for _, l := range lines {
		if !json.Valid([]byte(l)) {
			t.Errorf("not JSON: %s", l)
		}
	}
}

func TestLogsNotBooted(t *testing.T) {
	dir, f := logsFake(t)
	_, _, err := runLogs(t, dir, f, "--sim", "iPhone 17")
	if err == nil || !strings.Contains(err.Error(), `simulator "iPhone 17" is not booted`) {
		t.Errorf("err = %v", err)
	}
	if hasCommand(f, "xcrun simctl spawn") {
		t.Error("streamed from a simulator that isn't booted")
	}
}

func TestLogsLastOnSimulator(t *testing.T) {
	dir, f := logsFake(t)
	withOverride(f, "xcrun simctl spawn "+bootedUDID+" log show", xcode.FakeResult{Stdout: fixture(t, "log/stream.ndjson")})
	out, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "logs", "--last", "10m", "--level", "info")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"simctl", "spawn", bootedUDID, "log", "show", "--style", "ndjson", "--info",
		"--predicate", `subsystem == "` + testBundleID + `"`, "--last", "10m"}
	if got := commandArgs(t, f, "xcrun simctl spawn"); !slices.Equal(got, want) {
		t.Errorf("args:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(out, "Simulator is not supported") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestLogsDevice(t *testing.T) {
	dir, f := logsFake(t)
	_, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "logs", "--device")
	if err == nil || !strings.Contains(err.Error(), "use --last 5m") {
		t.Errorf("without --last: err = %v", err)
	}

	withOverride(f, "log show", xcode.FakeResult{Stdout: fixture(t, "log/stream.ndjson")})
	withOverride(f, "log collect", xcode.FakeResult{})
	out, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "logs", "--device", "--last", "5m")
	if err != nil {
		t.Fatal(err)
	}
	collect := commandArgs(t, f, "log collect")
	archive := argAfter(collect, "--output")
	if want := []string{"collect", "--device-udid", iPadUDID, "--last", "5m", "--output", archive}; !slices.Equal(collect, want) ||
		!strings.HasSuffix(archive, "/device.logarchive") {
		t.Errorf("collect args = %q", collect)
	}
	show := commandArgs(t, f, "log show")
	if show[len(show)-1] != archive || argAfter(show, "--predicate") != `subsystem == "`+testBundleID+`"` {
		t.Errorf("show args = %q", show)
	}
	if !strings.Contains(out, "Simulator is not supported") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestLogsDeviceCollectNeedsRoot(t *testing.T) {
	dir, f := logsFake(t)
	// Real output of Xcode 27.2.
	withOverride(f, "log collect", xcode.FakeResult{Err: &xcode.ExitError{
		Cmd: xcode.Cmd{Name: "log"}, Code: 77, Stderr: []byte("log: Must be root to collect logs from attached device\n"),
	}})
	_, errOut, err := runOpts(t, &options{workDir: dir, testRunner: f}, "logs", "--device", "--last", "5m")
	want := "hint: log collect needs root for a device. Run:\n  sudo log collect --device-udid " + iPadUDID +
		" --last 5m --output device.logarchive\n  runway logs --archive device.logarchive\n"
	if ec, ok := errors.AsType[ExitCoder](err); !ok || ec.ExitCode() != 77 || !strings.Contains(errOut, want) {
		t.Errorf("err = %v, stderr = %q", err, errOut)
	}
}

func TestLogsArchive(t *testing.T) {
	dir, f := logsFake(t)
	archive := filepath.Join(t.TempDir(), "my device.logarchive")
	if err := os.Mkdir(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	withOverride(f, "log show", xcode.FakeResult{Stdout: fixture(t, "log/stream.ndjson")})
	out, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "logs", "--archive", archive, "--level", "default")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := commandArgs(t, f, "log show"), []string{"show", "--style", "ndjson", "--predicate",
		`subsystem == "` + testBundleID + `"`, archive}; !slices.Equal(got, want) {
		t.Errorf("show args:\n got %q\nwant %q", got, want)
	}
	// The bundle ID is read without a destination: none was chosen.
	if settings := commandArgs(t, f, "xcodebuild -showBuildSettings"); slices.Contains(settings, "-destination") {
		t.Errorf("showBuildSettings args = %q", settings)
	}
	if hasCommand(f, "xcrun simctl list") || hasCommand(f, "xcrun devicectl") {
		t.Errorf("looked for a destination: %q", commandNames(f))
	}
	if !strings.Contains(out, "Simulator is not supported") {
		t.Errorf("stdout:\n%s", out)
	}

	_, _, err = runOpts(t, &options{workDir: dir, testRunner: f}, "logs", "--archive", filepath.Join(dir, "missing.logarchive"))
	if err == nil || !strings.Contains(err.Error(), "is not a .logarchive") {
		t.Errorf("missing archive: err = %v", err)
	}
	_, _, err = runOpts(t, &options{workDir: dir, testRunner: f}, "logs", "--archive", archive, "--sim")
	if err == nil || !strings.Contains(err.Error(), "none of the others can be") {
		t.Errorf("--archive --sim: err = %v", err)
	}
}

func TestLogsFlagErrors(t *testing.T) {
	dir, f := logsFake(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--level", "error"}, `invalid --level "error"`},
		{[]string{"--last", "5 minutes"}, `invalid --last "5 minutes"`},
		{[]string{"--destination", "platform=iOS Simulator,name=X"}, "logs needs --sim or --device"},
		{[]string{"--all", "--category", "x"}, "none of the others can be"},
		{[]string{"--predicate", "x", "--process", "y"}, "none of the others can be"},
	} {
		_, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, append([]string{"logs"}, tc.args...)...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want %q", tc.args, err, tc.want)
		}
	}
	if hasCommand(f, "xcrun simctl spawn") {
		t.Error("streamed despite invalid flags")
	}
}

func TestRunWithLogs(t *testing.T) {
	drain := logDrain
	logDrain = time.Millisecond
	t.Cleanup(func() { logDrain = drain })

	dir, appPath := runSetup(t, true)
	f := runFake(t, appPath, xcode.FakeResult{Stdout: []byte("hello from print\n")}, xcode.FakeResult{})
	withLogStream(t, f, fixture(t, "log/stream.ndjson"))
	out, errOut, err := runOpts(t, &options{workDir: dir, testRunner: f}, "run", "--sim", "--headless", "--logs")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	want := []string{
		"xcrun simctl bootstatus",
		"xcrun simctl install",
		"xcrun simctl spawn",
		"pgrep -f",
		"xcrun simctl launch",
		"kill -INT",
	}
	got := flow(f, 3)[2:] // after the build
	if len(got) != len(want) {
		t.Fatalf("flow:\n got %q\nwant %q", got, want)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("step %d = %q, want %q…", i, got[i], want[i])
		}
	}
	if p := streamPredicate(t, f); !strings.HasPrefix(p, `subsystem == "`+testBundleID+`" AND`) {
		t.Errorf("predicate = %s", p)
	}
	for _, s := range []string{"hello from print\n", "log │ ", "error   [Daemon] Simulator is not supported"} {
		if !strings.Contains(out, s) {
			t.Errorf("stdout is missing %q:\n%s", s, out)
		}
	}
	if strings.Contains(errOut, "warning") {
		t.Errorf("stopping the stream caused a warning:\n%s", errOut)
	}
}

func TestRunWithLogsCtrlC(t *testing.T) {
	dir, appPath := runSetup(t, true)
	f := runFake(t, appPath, xcode.FakeResult{Block: true}, xcode.FakeResult{})
	withLogStream(t, f, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := runOptsCtx(t, ctx, &options{workDir: dir, testRunner: f}, "run", "--sim", "--logs")
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !hasCommand(f, "xcrun simctl launch") {
		if time.Now().After(deadline) {
			t.Fatal("app was never launched")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after Ctrl-C")
	}
	got := flow(f, 3)
	if !slices.Equal(got[len(got)-2:], []string{"xcrun simctl terminate", "kill -INT " + logPID}) {
		t.Errorf("after Ctrl-C: %q, want the app terminated and then the log stream stopped", got)
	}
}

func TestRunWithLogsOnDevice(t *testing.T) {
	dir, appPath := runSetup(t, true)
	f := runFake(t, appPath, xcode.FakeResult{}, xcode.FakeResult{})
	_, errOut, err := runOpts(t, &options{workDir: dir, testRunner: f}, "run", "--device", "--logs", "--env", "A=1")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, c := range f.Calls() {
		if slices.Contains(c.Args, "launch") && !slices.Equal(c.Env, []string{"DEVICECTL_CHILD_A=1", "DEVICECTL_CHILD_OS_ACTIVITY_DT_MODE=1"}) {
			t.Errorf("launch env = %q", c.Env)
		}
	}
	if hasCommand(f, "xcrun simctl spawn") {
		t.Error("started a simulator log stream for a device")
	}
}
