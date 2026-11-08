package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stefankn/runway/internal/xcode"
)

const bootedSimUDID = "00000000-0000-4000-8000-00000000000B" // iPad (A16), booted in the fixture

// screenshotFake answers the simulator queries and records screenshots.
func screenshotFake(t *testing.T, shot xcode.FakeResult) *xcode.FakeRunner {
	t.Helper()
	return &xcode.FakeRunner{Handler: func(c xcode.Cmd) (xcode.FakeResult, bool) {
		switch line := c.Name + " " + strings.Join(c.Args, " "); {
		case strings.HasPrefix(line, "xcrun simctl list devices"):
			return xcode.FakeResult{Stdout: fixture(t, "simctl/devices-available.json")}, true
		case strings.HasPrefix(line, "xcrun simctl list runtimes"):
			return xcode.FakeResult{Stdout: fixture(t, "simctl/runtimes.json")}, true
		case strings.HasPrefix(line, "xcrun simctl io"):
			return shot, true
		}
		t.Errorf("unexpected command: %s", c)
		return xcode.FakeResult{}, false
	}}
}

func TestScreenshotBootedSimulator(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	orig := now
	now = func() time.Time { return time.Date(2026, 11, 8, 14, 5, 9, 0, time.UTC) }
	t.Cleanup(func() { now = orig })

	f := screenshotFake(t, xcode.FakeResult{})
	out, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "screenshot")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"simctl", "io", bootedSimUDID, "screenshot", "--type=png", filepath.Join(dir, "screenshot-20261108-140509.png")}
	if got := commandArgs(t, f, "xcrun simctl io"); !slices.Equal(got, want) {
		t.Errorf("args:\n got %q\nwant %q", got, want)
	}
	if out != "screenshot-20261108-140509.png\n" {
		t.Errorf("stdout = %q, want the path relative to the working folder", out)
	}
}

func TestScreenshotOutput(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	for _, tc := range []struct{ output, format, path string }{
		{"shots/home screen.jpg", "jpeg", filepath.Join(dir, "shots", "home screen.jpg")},
		{"A.TIFF", "tiff", filepath.Join(dir, "A.TIFF")},
		{"-.png", "png", filepath.Join(dir, "-.png")},
		{"/abs/x.gif", "gif", "/abs/x.gif"},
	} {
		f := screenshotFake(t, xcode.FakeResult{})
		if tc.output == "/abs/x.gif" {
			tc.path = filepath.Join(dir, "abs", "x.gif") // keep the test inside dir
			tc.output = tc.path
		}
		if _, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "screenshot", "--sim", "-o", tc.output); err != nil {
			t.Fatalf("%s: %v", tc.output, err)
		}
		got := commandArgs(t, f, "xcrun simctl io")
		if got[4] != "--type="+tc.format || got[5] != tc.path {
			t.Errorf("%s: args = %q", tc.output, got)
		}
		if fi, err := os.Stat(filepath.Dir(tc.path)); err != nil || !fi.IsDir() {
			t.Errorf("%s: folder was not created", tc.output)
		}
	}

	// Outside the working folder, the printed path is absolute.
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	f := screenshotFake(t, xcode.FakeResult{})
	out, _, err := runOpts(t, &options{workDir: sub, testRunner: f}, "screenshot", "-o", "../up.png")
	if want := filepath.Join(dir, "up.png") + "\n"; err != nil || out != want {
		t.Errorf("outside: out = %q, err = %v, want %q", out, err, want)
	}
}

func TestScreenshotNamedSimulator(t *testing.T) {
	clearEnv(t)
	f := screenshotFake(t, xcode.FakeResult{})
	if _, _, err := runOpts(t, &options{workDir: t.TempDir(), testRunner: f}, "screenshot", "--sim", "ipad (a16)"); err != nil {
		t.Fatal(err)
	}
	if got := commandArgs(t, f, "xcrun simctl io"); got[2] != bootedSimUDID {
		t.Errorf("args = %q", got)
	}
}

func TestScreenshotErrors(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--sim", "iPhone 17"}, `simulator "iPhone 17" is not booted`},
		{[]string{"--device"}, "simulators only"},
		{[]string{"--destination", "platform=iOS Simulator,name=X"}, "simulators only"},
		{[]string{"-o", "shot.webp"}, `can't save a screenshot as "shot.webp"`},
		{[]string{"-o", "shot"}, `can't save a screenshot as "shot"`},
	} {
		f := screenshotFake(t, xcode.FakeResult{})
		_, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, append([]string{"screenshot"}, tc.args...)...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want %q", tc.args, err, tc.want)
		}
		if slices.ContainsFunc(f.Calls(), func(c xcode.Cmd) bool { return len(c.Args) > 1 && c.Args[1] == "io" }) {
			t.Errorf("%q: took a screenshot anyway", tc.args)
		}
	}

	// A configured device is ignored: screenshot uses the booted simulator.
	t.Setenv("RUNWAY_DESTINATION", "device")
	f := screenshotFake(t, xcode.FakeResult{})
	if _, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "screenshot"); err != nil {
		t.Fatalf("configured device: %v", err)
	}
	if got := commandArgs(t, f, "xcrun simctl io"); got[2] != bootedSimUDID {
		t.Errorf("configured device: args = %q", got)
	}
}

func TestScreenshotSimctlFailure(t *testing.T) {
	clearEnv(t)
	f := screenshotFake(t, xcode.FakeResult{Err: &xcode.ExitError{Cmd: xcode.Cmd{Name: "xcrun"}, Code: 60, Stderr: []byte("Timeout waiting for screen surfaces")}})
	out, _, err := runOpts(t, &options{workDir: t.TempDir(), testRunner: f}, "screenshot")
	if ec, ok := errors.AsType[ExitCoder](err); !ok || ec.ExitCode() != 60 || !strings.Contains(err.Error(), "taking screenshot: ") {
		t.Errorf("err = %v", err)
	}
	if out != "" {
		t.Errorf("printed a path after a failure: %q", out)
	}
}
