package xcode

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseSimulatorsFixture(t *testing.T) {
	sims, err := parseSimulators(readFixture(t, "simctl/devices-available.json"), readFixture(t, "simctl/runtimes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sims) != 14 {
		t.Fatalf("got %d simulators, want 14", len(sims))
	}

	first := Simulator{
		UDID:      "00000000-0000-4000-8000-00000000000B",
		Name:      "iPad (A16)",
		State:     "Booted",
		Runtime:   "iOS 27.2",
		RuntimeID: "com.apple.CoreSimulator.SimRuntime.iOS-27-2",
		OSVersion: "27.2",
		Platform:  "iOS",
	}
	if sims[0] != first {
		t.Errorf("first simulator:\n got %+v\nwant %+v", sims[0], first)
	}
	if last := sims[len(sims)-1]; last.Platform != "tvOS" || last.Runtime != "tvOS 27.2" {
		t.Errorf("last simulator = %+v, want a tvOS 27.2 one", last)
	}

	// A device-specific runtime shares its display name with the general one.
	var duo Simulator
	booted := 0
	for _, s := range sims {
		if s.Name == "iPhone Duo" {
			duo = s
		}
		if s.Booted() {
			booted++
		}
	}
	if duo.RuntimeID != "com.apple.CoreSimulator.SimRuntime.iOS-27-2-iPhone19-4" || duo.Runtime != "iOS 27.2" {
		t.Errorf("iPhone Duo = %+v", duo)
	}
	if booted != 1 {
		t.Errorf("booted = %d, want 1", booted)
	}
}

func TestParseSimulatorsRuntimeFallback(t *testing.T) {
	devices := `{"devices": {
		"com.apple.CoreSimulator.SimRuntime.xrOS-2-0": [
			{"udid": "V1", "name": "Apple Vision Pro", "state": "Shutdown", "isAvailable": true}
		],
		"com.apple.CoreSimulator.SimRuntime.watchOS-11-4": [
			{"udid": "W1", "name": "Gone Watch", "state": "Shutdown", "isAvailable": false}
		],
		"com.apple.CoreSimulator.SimRuntime.iOS-18-2": []
	}}`
	sims, err := parseSimulators([]byte(devices), []byte(`{"runtimes": []}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Simulator{{
		UDID:      "V1",
		Name:      "Apple Vision Pro",
		State:     "Shutdown",
		Runtime:   "visionOS 2.0",
		RuntimeID: "com.apple.CoreSimulator.SimRuntime.xrOS-2-0",
		OSVersion: "2.0",
		Platform:  "visionOS",
	}}
	if !reflect.DeepEqual(sims, want) {
		t.Errorf("got %+v\nwant %+v", sims, want)
	}
}

func TestParseSimulatorsInvalidJSON(t *testing.T) {
	if _, err := parseSimulators([]byte("not json"), []byte(`{}`)); err == nil {
		t.Error("want error for invalid devices JSON")
	}
	if _, err := parseSimulators([]byte(`{}`), []byte("not json")); err == nil {
		t.Error("want error for invalid runtimes JSON")
	}
}

func TestListSimulatorsCommands(t *testing.T) {
	f := &FakeRunner{}
	f.Respond(FakeResult{Stdout: readFixture(t, "simctl/devices-available.json")}, "xcrun", "simctl", "list", "devices", "available", "-j")
	f.Respond(FakeResult{Stdout: readFixture(t, "simctl/runtimes.json")}, "xcrun", "simctl", "list", "runtimes", "-j")

	sims, err := New(f).ListSimulators(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sims) != 14 {
		t.Errorf("got %d simulators", len(sims))
	}
	var got [][]string
	for _, c := range f.Calls() {
		got = append(got, append([]string{c.Name}, c.Args...))
	}
	want := [][]string{
		{"xcrun", "simctl", "list", "devices", "available", "-j"},
		{"xcrun", "simctl", "list", "runtimes", "-j"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q, want %q", got, want)
	}
}

func TestListSimulatorsError(t *testing.T) {
	f := &FakeRunner{Default: FakeResult{Err: &ExitError{Cmd: xcrun("simctl"), Code: 72, Stderr: []byte("xcrun: error: unable to find utility \"simctl\"")}}}
	_, err := New(f).ListSimulators(context.Background())
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 72 {
		t.Fatalf("err = %v, want wrapped *ExitError with code 72", err)
	}
	if !strings.HasPrefix(err.Error(), "listing simulators: xcrun exited with status 72: xcrun: error") {
		t.Errorf("message = %q", err.Error())
	}
}

func TestSimLifecycleCommands(t *testing.T) {
	ctx := context.Background()
	f := &FakeRunner{}
	c := New(f)
	const udid = "SIM-1"
	if err := c.SimBoot(ctx, udid); err != nil {
		t.Fatal(err)
	}
	if err := c.SimInstall(ctx, udid, "/b/My App.app"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	p, err := c.SimLaunch(ctx, udid, "com.x.app", []string{"-flag", "two words"}, []string{"FOO=bar baz"}, ConsoleIO{Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := c.SimTerminate(ctx, udid, "com.x.app"); err != nil {
		t.Fatal(err)
	}
	if err := c.OpenDeviceHub(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.SimScreenshot(ctx, udid, "/shots/My Screen.jpg", "jpeg"); err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, cmd := range f.Calls() {
		got = append(got, cmd.String())
	}
	want := []string{
		"xcrun simctl bootstatus SIM-1 -b",
		"xcrun simctl install SIM-1 '/b/My App.app'",
		"SIMCTL_CHILD_FOO='bar baz' xcrun simctl launch --console-pty --terminate-running-process SIM-1 com.x.app -flag 'two words'",
		"xcrun simctl terminate SIM-1 com.x.app",
		"open -b com.apple.dt.Devices",
		"xcrun simctl io SIM-1 screenshot --type=jpeg '/shots/My Screen.jpg'",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n got %q\nwant %q", got, want)
	}
	if launch := f.Calls()[2]; launch.Stdout != &out {
		t.Error("launch output is not connected to the console writer")
	}
}

func TestSimWrappersWrapErrors(t *testing.T) {
	ctx := context.Background()
	fail := &ExitError{Cmd: Cmd{Name: "xcrun"}, Code: 149, Stderr: []byte("Invalid device: SIM-1")}
	c := New(&FakeRunner{Default: FakeResult{Err: fail, StartErr: errors.New("no simctl")}})
	for _, tc := range []struct {
		err  error
		want string
	}{
		{c.SimBoot(ctx, "SIM-1"), "booting simulator: "},
		{c.SimInstall(ctx, "SIM-1", "/a.app"), "installing app: "},
		{c.SimTerminate(ctx, "SIM-1", "x"), "stopping app: "},
		{c.OpenDeviceHub(ctx), "opening DeviceHub: "},
		{c.SimScreenshot(ctx, "SIM-1", "/a.png", "png"), "taking screenshot: "},
	} {
		if tc.err == nil || !strings.HasPrefix(tc.err.Error(), tc.want) {
			t.Errorf("err = %v, want prefix %q", tc.err, tc.want)
		}
	}
	if _, err := c.SimLaunch(ctx, "SIM-1", "x", nil, nil, ConsoleIO{}); err == nil || !strings.HasPrefix(err.Error(), "launching app: ") {
		t.Errorf("launch err = %v", err)
	}
}
