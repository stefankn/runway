package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stefankn/runway/internal/xcode"
)

// fixture reads a file from the xcode package's testdata.
func fixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile("../xcode/testdata/" + path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// runWith runs runway with args against a fake runner.
func runWith(t *testing.T, fake *xcode.FakeRunner, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runOpts(t, &options{testRunner: fake}, args...)
}

// runOpts runs runway with args and the given options (test hooks).
func runOpts(t *testing.T, opts *options, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runOptsCtx(t, context.Background(), opts, args...)
}

// runOptsCtx is runOpts with a context, e.g. one the test cancels to
// simulate Ctrl-C.
func runOptsCtx(t *testing.T, ctx context.Context, opts *options, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut syncBuffer
	root := newRootCommand(testInfo, opts)
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&errOut)
	err = root.ExecuteContext(ctx)
	return out.String(), errOut.String(), err
}

// syncBuffer is a bytes.Buffer that is safe for concurrent writes, since
// child process output is copied in separate goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func simulatorsFake(t *testing.T) *xcode.FakeRunner {
	f := &xcode.FakeRunner{}
	f.Respond(xcode.FakeResult{Stdout: fixture(t, "simctl/devices-available.json")}, "xcrun", "simctl", "list", "devices", "available", "-j")
	f.Respond(xcode.FakeResult{Stdout: fixture(t, "simctl/runtimes.json")}, "xcrun", "simctl", "list", "runtimes", "-j")
	return f
}

func lines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func TestSimulatorsTable(t *testing.T) {
	out, _, err := runWith(t, simulatorsFake(t), "simulators")
	if err != nil {
		t.Fatal(err)
	}
	l := lines(out)
	if len(l) != 15 {
		t.Fatalf("got %d lines, want header + 14:\n%s", len(l), out)
	}
	if !strings.HasPrefix(l[0], "NAME") || !strings.Contains(l[0], "RUNTIME") {
		t.Errorf("header = %q", l[0])
	}
	if want := "iPad (A16)                               iOS 27.2   Booted    00000000-0000-4000-8000-00000000000B"; l[1] != want {
		t.Errorf("first row:\n got %q\nwant %q", l[1], want)
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("colour codes written to a non-terminal")
	}
}

func TestSimulatorsFilters(t *testing.T) {
	for _, tc := range []struct {
		args []string
		rows int
	}{
		{[]string{"--booted"}, 1},
		{[]string{"--platform", "tvos"}, 2},
		{[]string{"--platform", "TVOS"}, 2},
		{[]string{"--platform", "ios"}, 12},
		{[]string{"--runtime", "27"}, 14},
		{[]string{"--runtime", "27.2", "--platform", "ios", "--booted"}, 1},
	} {
		out, _, err := runWith(t, simulatorsFake(t), append([]string{"simulators"}, tc.args...)...)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := len(lines(out)) - 1; got != tc.rows {
			t.Errorf("%v: %d rows, want %d", tc.args, got, tc.rows)
		}
	}
}

func TestSimulatorsNoMatch(t *testing.T) {
	out, errOut, err := runWith(t, simulatorsFake(t), "simulators", "--runtime", "18")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" || !strings.Contains(errOut, "No simulators match") {
		t.Errorf("stdout = %q, stderr = %q", out, errOut)
	}
}

func TestSimulatorsUnknownPlatform(t *testing.T) {
	_, _, err := runWith(t, simulatorsFake(t), "simulators", "--platform", "macos")
	if err == nil || !strings.Contains(err.Error(), `unknown platform "macos"`) {
		t.Errorf("err = %v", err)
	}
}

func TestSimulatorsJSON(t *testing.T) {
	out, _, err := runWith(t, simulatorsFake(t), "simulators", "--json", "--platform", "tvos")
	if err != nil {
		t.Fatal(err)
	}
	var sims []xcode.Simulator
	if err := json.Unmarshal([]byte(out), &sims); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(sims) != 2 || sims[0].Platform != "tvOS" || sims[0].UDID == "" {
		t.Errorf("sims = %+v", sims)
	}

	out, _, err = runWith(t, simulatorsFake(t), "simulators", "--json", "--runtime", "18")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty JSON = %q, want []", out)
	}
}
