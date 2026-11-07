package xcode

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

var testBuild = BuildOptions{
	Container:     Container{Workspace, "/p/My App.xcworkspace"},
	Scheme:        "MyApp",
	Configuration: "Debug",
	Destination:   "platform=iOS Simulator,id=SIM-1",
	DerivedData:   "/p/.build/runway",
}

func TestBuildArgs(t *testing.T) {
	want := []string{
		"build", "-workspace", "/p/My App.xcworkspace",
		"-scheme", "MyApp", "-configuration", "Debug",
		"-destination", "platform=iOS Simulator,id=SIM-1",
		"-derivedDataPath", "/p/.build/runway",
		"-allowProvisioningUpdates",
	}
	if got := BuildArgs(testBuild); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}

	o := testBuild
	o.Container = Container{Project, "App.xcodeproj"}
	o.Quiet, o.Team, o.ExtraArgs = true, "TEAM1", []string{"-showBuildTimingSummary", "SWIFT_VERSION=6"}
	got := BuildArgs(o)
	if got[1] != "-project" || got[2] != "App.xcodeproj" {
		t.Errorf("container args = %q", got[1:3])
	}
	if tail := got[len(got)-5:]; !reflect.DeepEqual(tail, []string{"-allowProvisioningUpdates", "-quiet", "DEVELOPMENT_TEAM=TEAM1", "-showBuildTimingSummary", "SWIFT_VERSION=6"}) {
		t.Errorf("tail = %q", tail)
	}
}

func TestBuildRaw(t *testing.T) {
	f := &FakeRunner{Default: FakeResult{Stdout: []byte("** BUILD SUCCEEDED **\n")}}
	var out, tap bytes.Buffer
	err := New(f).Build(context.Background(), testBuild, BuildOutput{Stdout: &out, Stderr: &out, Tap: &tap})
	if err != nil {
		t.Fatal(err)
	}
	if calls := f.Calls(); len(calls) != 1 || calls[0].Name != "xcodebuild" {
		t.Fatalf("calls = %v", calls)
	}
	if out.String() != "** BUILD SUCCEEDED **\n" || tap.String() != out.String() {
		t.Errorf("out = %q, tap = %q", out.String(), tap.String())
	}
}

func TestBuildBeautified(t *testing.T) {
	o := testBuild
	o.Quiet = true
	f := &FakeRunner{}
	f.Respond(FakeResult{Stdout: []byte("raw line\n"), Err: &ExitError{Cmd: Cmd{Name: "xcodebuild"}, Code: 65}}, "xcodebuild", BuildArgs(o)...)
	f.Respond(FakeResult{EchoStdin: true}, "xcbeautify", "--disable-logging", "--quieter", "--disable-colored-output")

	var out, tap bytes.Buffer
	err := New(f).Build(context.Background(), o, BuildOutput{Stdout: &out, Stderr: &out, Beautify: true, Tap: &tap})

	var ee *ExitError
	if !errors.Is(err, ErrBuildFailed) || !errors.As(err, &ee) || ee.Code != 65 {
		t.Fatalf("err = %v, want ErrBuildFailed wrapping exit 65", err)
	}
	if out.String() != "raw line\n" {
		t.Errorf("formatted output = %q (should arrive through xcbeautify)", out.String())
	}
	if tap.String() != "raw line\n" {
		t.Errorf("tap = %q", tap.String())
	}
	var names []string
	for _, c := range f.Calls() {
		names = append(names, c.String())
	}
	if len(names) != 2 || names[0] != "xcbeautify --disable-logging --quieter --disable-colored-output" || !strings.HasPrefix(names[1], "xcodebuild build ") {
		t.Errorf("commands = %q", names)
	}
}

func TestBuildBeautifiedColor(t *testing.T) {
	f := &FakeRunner{Default: FakeResult{EchoStdin: true}}
	var out bytes.Buffer
	if err := New(f).Build(context.Background(), testBuild, BuildOutput{Stdout: &out, Stderr: &out, Beautify: true, Color: true}); err != nil {
		t.Fatal(err)
	}
	if got := f.Calls()[0].String(); got != "xcbeautify --disable-logging" {
		t.Errorf("xcbeautify command = %q", got)
	}
}

func TestParseBuildSettings(t *testing.T) {
	app, err := parseBuildSettings(readFixture(t, "xcodebuild/build-settings.json"), "Sensors")
	if err != nil {
		t.Fatal(err)
	}
	want := AppProduct{
		Path:     "/Users/tester/MyProject/.build/runway/Build/Products/Debug-iphonesimulator/Sensors.app",
		BundleID: "com.github.example.Sensors",
		Target:   "Sensors",
		Platform: "iphonesimulator",
	}
	if app != want {
		t.Errorf("got %+v\nwant %+v", app, want)
	}
}

func TestParseBuildSettingsChoosesApp(t *testing.T) {
	doc := `[
		{"target": "MyAppTests", "buildSettings": {"WRAPPER_EXTENSION": "xctest", "WRAPPER_NAME": "MyAppTests.xctest", "TARGET_BUILD_DIR": "/b"}},
		{"target": "Companion", "buildSettings": {"WRAPPER_EXTENSION": "app", "WRAPPER_NAME": "Companion.app", "TARGET_BUILD_DIR": "/b", "PRODUCT_BUNDLE_IDENTIFIER": "com.x.companion"}},
		{"target": "MyApp", "buildSettings": {"WRAPPER_EXTENSION": "app", "WRAPPER_NAME": "MyApp.app", "TARGET_BUILD_DIR": "/b", "PRODUCT_BUNDLE_IDENTIFIER": "com.x.app"}}
	]`
	app, err := parseBuildSettings([]byte(doc), "MyApp")
	if err != nil || app.BundleID != "com.x.app" || app.Path != "/b/MyApp.app" {
		t.Errorf("scheme-named target: %+v, %v", app, err)
	}
	app, err = parseBuildSettings([]byte(doc), "Other")
	if err != nil || app.Target != "Companion" {
		t.Errorf("first app fallback: %+v, %v", app, err)
	}
	_, err = parseBuildSettings([]byte(`[{"target": "Lib", "buildSettings": {"WRAPPER_EXTENSION": "framework"}}]`), "Lib")
	if err == nil || !strings.Contains(err.Error(), "doesn't build an app") {
		t.Errorf("no app: %v", err)
	}
	if _, err := parseBuildSettings([]byte("nope"), "X"); err == nil {
		t.Error("invalid JSON: want error")
	}
}

func TestAppProductArgs(t *testing.T) {
	f := &FakeRunner{Default: FakeResult{Stdout: readFixture(t, "xcodebuild/build-settings.json")}}
	o := testBuild
	o.Team = "TEAM1"
	o.ExtraArgs = []string{"-quiet", "SYMROOT=/tmp/sym", "-only-testing:X"}
	if _, err := New(f).AppProduct(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	want := "xcodebuild -showBuildSettings -json -workspace '/p/My App.xcworkspace' -scheme MyApp -configuration Debug " +
		"-destination 'platform=iOS Simulator,id=SIM-1' -derivedDataPath /p/.build/runway SYMROOT=/tmp/sym"
	if got := f.Calls()[0].String(); got != want {
		t.Errorf("command:\n got %s\nwant %s", got, want)
	}
}

func TestDestinationStrings(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{SimulatorDestination(Simulator{UDID: "S1", Platform: "iOS"}), "platform=iOS Simulator,id=S1"},
		{SimulatorDestination(Simulator{UDID: "S2", Platform: "visionOS"}), "platform=visionOS Simulator,id=S2"},
		{DeviceDestination(Device{UDID: "D1", Platform: "iOS"}), "platform=iOS,id=D1"},
		{DeviceDestination(Device{UDID: "D2", Platform: "tvOS"}), "platform=tvOS,id=D2"},
		{DeviceDestination(Device{UDID: "D3", Platform: "xrOS"}), "platform=visionOS,id=D3"},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}
