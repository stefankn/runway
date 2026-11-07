package xcode

import (
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
