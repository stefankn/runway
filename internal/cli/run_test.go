package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stefankn/runway/internal/xcode"
)

const testBundleID = "com.example.ActorTest"

// runSetup creates a project folder with ActorTest.xcodeproj and, if
// withApp, a built ActorTest.app. It returns the folder and the app's path.
func runSetup(t *testing.T, withApp bool) (dir, appPath string) {
	t.Helper()
	clearEnv(t)
	dir = projectDir(t, []string{"ActorTest.xcodeproj"}, nil)
	appPath = filepath.Join(dir, ".build", "runway", "Build", "Products", "Debug-iphonesimulator", "ActorTest.app")
	if withApp {
		if err := os.MkdirAll(appPath, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir, appPath
}

// runFake answers every command `run` makes. launch is the result of
// `simctl launch`; terminate of `simctl terminate`.
func runFake(t *testing.T, appPath string, launch, terminate xcode.FakeResult) *xcode.FakeRunner {
	t.Helper()
	settings := fmt.Sprintf(`[{"target": "ActorTest", "buildSettings": {"WRAPPER_EXTENSION": "app", "WRAPPER_NAME": "ActorTest.app",
		"TARGET_BUILD_DIR": %q, "PRODUCT_BUNDLE_IDENTIFIER": %q}}]`, filepath.Dir(appPath), testBundleID)
	answers := []struct {
		prefix string
		result xcode.FakeResult
	}{
		{"xcodebuild -list", xcode.FakeResult{Stdout: fixture(t, "xcodebuild/list-project.json")}},
		{"xcodebuild -showBuildSettings", xcode.FakeResult{Stdout: []byte(settings)}},
		{"xcodebuild build", buildOK},
		{"xcrun simctl list devices", xcode.FakeResult{Stdout: fixture(t, "simctl/devices-available.json")}},
		{"xcrun simctl list runtimes", xcode.FakeResult{Stdout: fixture(t, "simctl/runtimes.json")}},
		{"xcrun devicectl list devices", xcode.FakeResult{Stdout: fixture(t, "devicectl/list-devices.json")}},
		{"xcrun simctl bootstatus", xcode.FakeResult{}},
		{"xcrun simctl install", xcode.FakeResult{}},
		{"xcrun simctl launch", launch},
		{"xcrun simctl terminate", terminate},
		{"open -b", xcode.FakeResult{}},
	}
	return &xcode.FakeRunner{Handler: func(c xcode.Cmd) (xcode.FakeResult, bool) {
		line := c.Name + " " + strings.Join(c.Args, " ")
		for _, a := range answers {
			if strings.HasPrefix(line, a.prefix) {
				return a.result, true
			}
		}
		t.Errorf("unexpected command: %s", c)
		return xcode.FakeResult{}, false
	}}
}

// commandNames returns the recorded commands as "name sub" (e.g. "xcrun simctl install").
func commandNames(f *xcode.FakeRunner) []string {
	var names []string
	for _, c := range f.Calls() {
		parts := append([]string{c.Name}, c.Args...)
		names = append(names, strings.Join(parts[:min(3, len(parts))], " "))
	}
	return names
}

func hasCommand(f *xcode.FakeRunner, prefix string) bool {
	return slices.ContainsFunc(commandNames(f), func(n string) bool { return strings.HasPrefix(n, prefix) })
}

func TestRunFlow(t *testing.T) {
	dir, appPath := runSetup(t, true)
	f := runFake(t, appPath, xcode.FakeResult{Stdout: []byte("hello from the app\n")}, xcode.FakeResult{})
	out, errOut, err := runOpts(t, &options{workDir: dir, testRunner: f}, "run", "--sim", "--", "-flag", "two words")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}

	var flow []string
	for _, n := range commandNames(f) {
		if !strings.Contains(n, "list") { // skip the discovery queries

			flow = append(flow, n)
		}
	}
	want := []string{
		"xcodebuild build -project",
		"xcodebuild -showBuildSettings -json",
		"xcrun simctl bootstatus",
		"open -b com.apple.dt.Devices",
		"xcrun simctl install",
		"xcrun simctl launch",
	}
	if !slices.Equal(flow, want) {
		t.Errorf("flow:\n got %q\nwant %q", flow, want)
	}

	launch := commandArgs(t, f, "xcrun simctl launch")
	wantLaunch := []string{"simctl", "launch", "--console-pty", "--terminate-running-process",
		"00000000-0000-4000-8000-000000000005", testBundleID, "-flag", "two words"}
	if !slices.Equal(launch, wantLaunch) {
		t.Errorf("launch args:\n got %q\nwant %q", launch, wantLaunch)
	}
	if got := commandArgs(t, f, "xcrun simctl install"); got[3] != appPath {
		t.Errorf("installed %q, want %q", got[3], appPath)
	}
	if !strings.Contains(out, "hello from the app") {
		t.Errorf("app output missing from stdout: %q", out)
	}
	for _, s := range []string{"Booting iPhone 17…", "Installing ActorTest.app…", "Launching " + testBundleID + " on iPhone 17 (Ctrl-C to stop)"} {
		if !strings.Contains(errOut, s) {
			t.Errorf("stderr is missing %q:\n%s", s, errOut)
		}
	}
	if hasCommand(f, "xcrun simctl terminate") {
		t.Error("app exited by itself; it should not be terminated")
	}
}

func TestRunBootedSimulatorSkipsBootMessage(t *testing.T) {
	dir, appPath := runSetup(t, true)
	f := runFake(t, appPath, xcode.FakeResult{}, xcode.FakeResult{})
	_, errOut, err := runOpts(t, &options{workDir: dir, testRunner: f}, "run", "--sim", "iPad (A16)") // booted in the fixture
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "Booting") {
		t.Errorf("booted simulator: stderr = %q", errOut)
	}
	if !hasCommand(f, "xcrun simctl bootstatus") {
		t.Error("bootstatus should still run (it waits for a booting simulator)")
	}
}

func TestRunHeadlessAndEnv(t *testing.T) {
	dir, appPath := runSetup(t, true)
	f := runFake(t, appPath, xcode.FakeResult{}, xcode.FakeResult{})
	_, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "run", "--sim", "--headless", "--env", "API_URL=http://x:1/a,b", "--env", "DEBUG=1")
	if err != nil {
		t.Fatal(err)
	}
	if hasCommand(f, "open") {
		t.Error("--headless should not open DeviceHub")
	}
	var env []string
	for _, c := range f.Calls() {
		if c.Name == "xcrun" && len(c.Args) > 1 && c.Args[1] == "launch" {
			env = c.Env
		}
	}
	if want := []string{"SIMCTL_CHILD_API_URL=http://x:1/a,b", "SIMCTL_CHILD_DEBUG=1"}; !slices.Equal(env, want) {
		t.Errorf("launch env = %q, want %q", env, want)
	}

	for _, bad := range []string{"NOVALUE", "=x", "1X=y", "A B=c"} {
		_, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "run", "--sim", "--env", bad)
		if err == nil || !strings.Contains(err.Error(), "invalid --env") {
			t.Errorf("--env %q: err = %v", bad, err)
		}
	}
}

func TestRunNoBuild(t *testing.T) {
	dir, appPath := runSetup(t, true)
	f := runFake(t, appPath, xcode.FakeResult{}, xcode.FakeResult{})
	if _, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "run", "--sim", "--no-build"); err != nil {
		t.Fatal(err)
	}
	if hasCommand(f, "xcodebuild build") {
		t.Error("--no-build ran a build")
	}

	dir, appPath = runSetup(t, false)
	f = runFake(t, appPath, xcode.FakeResult{}, xcode.FakeResult{})
	_, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "run", "--sim", "--no-build")
	if err == nil || !strings.Contains(err.Error(), "no build found at .build/runway/") {
		t.Errorf("missing app: err = %v", err)
	}
}

func TestRunAppExitStatus(t *testing.T) {
	dir, appPath := runSetup(t, true)
	launch := xcode.FakeResult{Err: &xcode.ExitError{Cmd: xcode.Cmd{Name: "xcrun"}, Code: 3}}
	f := runFake(t, appPath, launch, xcode.FakeResult{})
	_, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "run", "--sim")
	ec, ok := errors.AsType[ExitCoder](err)
	if !ok || ec.ExitCode() != 3 || err.Error() != "app exited with status 3" {
		t.Errorf("err = %v, want exit status 3", err)
	}
	if hasCommand(f, "xcrun simctl terminate") {
		t.Error("app exited by itself; it should not be terminated")
	}
}

func TestRunCtrlC(t *testing.T) {
	for _, terminate := range []xcode.FakeResult{
		{},
		{Err: errors.New("found nothing to terminate")}, // the app already quit on SIGINT
	} {
		dir, appPath := runSetup(t, true)
		f := runFake(t, appPath, xcode.FakeResult{Block: true}, terminate)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, _, err := runOptsCtx(t, ctx, &options{workDir: dir, testRunner: f}, "run", "--sim")
			done <- err
		}()

		deadline := time.Now().Add(5 * time.Second)
		for !hasCommand(f, "xcrun simctl launch") {
			if time.Now().After(deadline) {
				t.Fatal("app was never launched")
			}
			time.Sleep(5 * time.Millisecond)
		}
		cancel() // Ctrl-C

		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, want context.Canceled (exit 130)", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("run did not return after Ctrl-C")
		}
		if got := commandArgs(t, f, "xcrun simctl terminate"); !slices.Equal(got, []string{"simctl", "terminate", "00000000-0000-4000-8000-000000000005", testBundleID}) {
			t.Errorf("terminate args = %q", got)
		}
	}
}

func TestRunRejectsOtherDestinations(t *testing.T) {
	dir, appPath := runSetup(t, true)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--device"}, "running on devices arrives in the next milestone"},
		{[]string{"--destination", "platform=iOS Simulator,name=X"}, "run needs --sim or --device"},
	} {
		f := runFake(t, appPath, xcode.FakeResult{}, xcode.FakeResult{})
		_, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, append([]string{"run"}, tc.args...)...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v", tc.args, err)
		}
		if hasCommand(f, "xcodebuild build") {
			t.Errorf("%q: built before rejecting the destination", tc.args)
		}
	}
}

func TestRunBuildFailureStopsBeforeBoot(t *testing.T) {
	dir, appPath := runSetup(t, true)
	f := runFake(t, appPath, xcode.FakeResult{}, xcode.FakeResult{})
	inner := f.Handler
	f.Handler = func(c xcode.Cmd) (xcode.FakeResult, bool) {
		if c.Name == "xcodebuild" && len(c.Args) > 0 && c.Args[0] == "build" {
			return xcode.FakeResult{Err: &xcode.ExitError{Cmd: c, Code: 65}}, true
		}
		return inner(c)
	}
	_, _, err := runOpts(t, &options{workDir: dir, testRunner: f}, "run", "--sim")
	if ec, ok := errors.AsType[ExitCoder](err); !ok || ec.ExitCode() != 65 {
		t.Errorf("err = %v, want the build's exit status 65", err)
	}
	if hasCommand(f, "xcrun simctl bootstatus") || hasCommand(f, "xcrun simctl launch") {
		t.Errorf("continued after a failed build: %q", commandNames(f))
	}
}
