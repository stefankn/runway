package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stefankn/runway/internal/xcode"
)

// buildFake answers runway's commands for a folder containing ActorTest.xcodeproj:
// the scheme list, simulators, devices, build settings, and the build itself
// (with buildResult). xcbeautify echoes its input.
func buildFake(t *testing.T, buildResult xcode.FakeResult) *xcode.FakeRunner {
	t.Helper()
	answers := map[string][]byte{
		"xcodebuild -list":              fixture(t, "xcodebuild/list-project.json"),
		"xcodebuild -showBuildSettings": fixture(t, "xcodebuild/build-settings.json"),
		"xcrun simctl list devices":     fixture(t, "simctl/devices-available.json"),
		"xcrun simctl list runtimes":    fixture(t, "simctl/runtimes.json"),
		"xcrun devicectl list devices":  fixture(t, "devicectl/list-devices.json"),
	}
	return &xcode.FakeRunner{Handler: func(c xcode.Cmd) (xcode.FakeResult, bool) {
		line := c.Name + " " + strings.Join(c.Args, " ")
		switch {
		case c.Name == "xcbeautify":
			return xcode.FakeResult{EchoStdin: true}, true
		case strings.HasPrefix(line, "xcodebuild build "):
			return buildResult, true
		}
		for prefix, out := range answers {
			if strings.HasPrefix(line, prefix) {
				return xcode.FakeResult{Stdout: out}, true
			}
		}
		t.Errorf("unexpected command: %s", c)
		return xcode.FakeResult{}, false
	}}
}

var buildOK = xcode.FakeResult{Stdout: []byte("** BUILD SUCCEEDED **\n")}

// commandArgs returns the arguments of the first recorded command whose
// command line starts with prefix.
func commandArgs(t *testing.T, f *xcode.FakeRunner, prefix string) []string {
	t.Helper()
	for _, c := range f.Calls() {
		if strings.HasPrefix(c.Name+" "+strings.Join(c.Args, " "), prefix) {
			return c.Args
		}
	}
	t.Fatalf("no %q command in %v", prefix, f.Calls())
	return nil
}

// argAfter returns the argument following flag.
func argAfter(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

func runBuild(t *testing.T, f *xcode.FakeRunner, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	clearEnv(t)
	dir := projectDir(t, []string{"ActorTest.xcodeproj"}, nil)
	return runOpts(t, &options{workDir: dir, testRunner: f}, append([]string{"build"}, args...)...)
}

func TestBuildDefaultSimulator(t *testing.T) {
	f := buildFake(t, buildOK)
	out, errOut, err := runBuild(t, f, "--sim")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	args := commandArgs(t, f, "xcodebuild build")
	if got := argAfter(args, "-destination"); got != "platform=iOS Simulator,id=00000000-0000-4000-8000-000000000005" {
		t.Errorf("destination = %q, want the default iPhone 17", got)
	}
	if argAfter(args, "-scheme") != "ActorTest" || argAfter(args, "-configuration") != "Debug" ||
		!strings.HasSuffix(argAfter(args, "-project"), "/ActorTest.xcodeproj") ||
		!strings.HasSuffix(argAfter(args, "-derivedDataPath"), "/.build/runway") {
		t.Errorf("build args = %q", args)
	}
	if !strings.Contains(errOut, "Building ActorTest (Debug) for iPhone 17 (iOS 27.2 Simulator)") {
		t.Errorf("stderr = %q", errOut)
	}
	for _, want := range []string{"** BUILD SUCCEEDED **", "Build succeeded", "Sensors.app", "Bundle ID: com.github.example.Sensors"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout is missing %q:\n%s", want, out)
		}
	}
	for _, c := range f.Calls() {
		if c.Name == "xcbeautify" {
			t.Error("xcbeautify ran although it isn't installed")
		}
	}
}

func TestBuildSimFlagForms(t *testing.T) {
	const air = "platform=iOS Simulator,id=00000000-0000-4000-8000-000000000004"
	for _, args := range [][]string{
		{"--sim", "iPhone Air"},
		{"--sim=iPhone Air"},
		{"--sim", "iphone air", "--", "-showBuildTimingSummary"},
		{"--destination", air},
	} {
		f := buildFake(t, buildOK)
		if _, errOut, err := runBuild(t, f, args...); err != nil {
			t.Fatalf("%q: %v\n%s", args, err, errOut)
		}
		if got := argAfter(commandArgs(t, f, "xcodebuild build"), "-destination"); got != air {
			t.Errorf("%q: destination = %q", args, got)
		}
	}
}

func TestBuildStrayArguments(t *testing.T) {
	for _, args := range [][]string{
		{"iPhone"},
		{"--sim", "a", "b"},
		{"--device", "x", "--", "y"}, // ok: x is the device…
	} {
		_, _, err := runBuild(t, buildFake(t, buildOK), args...)
		if args[0] == "--device" {
			// …but there's no device called x.
			if err == nil || !strings.Contains(err.Error(), `no device matches "x"`) {
				t.Errorf("%q: err = %v", args, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
			t.Errorf("%q: err = %v", args, err)
		}
	}
	if _, _, err := runBuild(t, buildFake(t, buildOK), "--sim", "x", "--device"); err == nil ||
		!strings.Contains(err.Error(), "none of the others can be") {
		t.Errorf("--sim with --device: err = %v", err)
	}
}

func TestBuildDeviceAndTeam(t *testing.T) {
	f := buildFake(t, buildOK)
	if _, errOut, err := runBuild(t, f, "--device", "--team", "TEAM1"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	args := commandArgs(t, f, "xcodebuild build")
	if got := argAfter(args, "-destination"); got != "platform=iOS,id=00008103-0000000000000008" {
		t.Errorf("destination = %q, want the wired iPad", got)
	}
	if !slices.Contains(args, "DEVELOPMENT_TEAM=TEAM1") || !slices.Contains(args, "-allowProvisioningUpdates") {
		t.Errorf("build args = %q", args)
	}
}

func TestBuildPassthrough(t *testing.T) {
	f := buildFake(t, buildOK)
	if _, _, err := runBuild(t, f, "--sim", "--", "-showBuildTimingSummary", "SWIFT_VERSION=6"); err != nil {
		t.Fatal(err)
	}
	build := commandArgs(t, f, "xcodebuild build")
	if tail := build[len(build)-2:]; !slices.Equal(tail, []string{"-showBuildTimingSummary", "SWIFT_VERSION=6"}) {
		t.Errorf("build tail = %q", tail)
	}
	settings := commandArgs(t, f, "xcodebuild -showBuildSettings")
	if !slices.Contains(settings, "SWIFT_VERSION=6") || slices.Contains(settings, "-showBuildTimingSummary") {
		t.Errorf("showBuildSettings args = %q", settings)
	}
}

func TestBuildBeautify(t *testing.T) {
	for _, tc := range []struct {
		args         []string
		wantFormat   string // xcbeautify command line, "" = not run
		wantQuietArg bool
	}{
		{nil, "xcbeautify --disable-logging --disable-colored-output", false},
		{[]string{"--quiet"}, "xcbeautify --disable-logging --quieter --disable-colored-output", true},
		{[]string{"--raw"}, "", false},
	} {
		f := buildFake(t, buildOK)
		f.Paths = map[string]string{"xcbeautify": "/opt/homebrew/bin/xcbeautify"}
		out, errOut, err := runBuild(t, f, append([]string{"--sim"}, tc.args...)...)
		if err != nil {
			t.Fatalf("%q: %v", tc.args, err)
		}
		var format string
		for _, c := range f.Calls() {
			if c.Name == "xcbeautify" {
				format = c.String()
			}
		}
		if format != tc.wantFormat {
			t.Errorf("%q: xcbeautify = %q, want %q", tc.args, format, tc.wantFormat)
		}
		if got := slices.Contains(commandArgs(t, f, "xcodebuild build"), "-quiet"); got != tc.wantQuietArg {
			t.Errorf("%q: -quiet passed = %v", tc.args, got)
		}
		if !strings.Contains(out, "** BUILD SUCCEEDED **") {
			t.Errorf("%q: build output missing: %q", tc.args, out)
		}
		if quiet := tc.wantQuietArg; quiet == strings.Contains(errOut, "Building ") {
			t.Errorf("%q: status line shown = %v", tc.args, !quiet)
		}
		// runway's own "Build succeeded" only when xcbeautify doesn't print one.
		wantSummary := tc.wantFormat == "" || tc.wantQuietArg
		if got := strings.Contains(out, "Build succeeded"); got != wantSummary {
			t.Errorf("%q: runway's success line shown = %v, want %v", tc.args, got, wantSummary)
		}
		if !strings.Contains(out, "Bundle ID: com.github.example.Sensors") {
			t.Errorf("%q: app summary missing:\n%s", tc.args, out)
		}
	}
}

func TestBuildFailureHints(t *testing.T) {
	fail := xcode.FakeResult{
		Stdout: []byte("error: Signing for \"ActorTest\" requires a development team. Select a development team in the Signing & Capabilities editor.\n** BUILD FAILED **\n"),
		Err:    &xcode.ExitError{Cmd: xcode.Cmd{Name: "xcodebuild"}, Code: 65},
	}
	f := buildFake(t, fail)
	out, errOut, err := runBuild(t, f, "--device")
	var ee *xcode.ExitError
	if !errors.As(err, &ee) || ee.Code != 65 {
		t.Fatalf("err = %v, want exit status 65", err)
	}
	if !strings.Contains(out, "requires a development team") {
		t.Errorf("xcodebuild's error should be shown as-is: %q", out)
	}
	if !strings.Contains(errOut, "hint: Set team in .runway.toml or pass --team") {
		t.Errorf("stderr = %q", errOut)
	}
	if strings.Contains(out, "Build succeeded") {
		t.Error("success summary printed after a failure")
	}
}
