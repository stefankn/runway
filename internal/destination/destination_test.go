package destination

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

func fixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile("../xcode/testdata/" + path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// client returns a Client whose simctl and devicectl answers come from the
// given JSON (fixtures by default).
func client(t *testing.T, simDevices, runtimes, devices []byte) (*xcode.Client, *xcode.FakeRunner) {
	t.Helper()
	if simDevices == nil {
		simDevices = fixture(t, "simctl/devices-available.json")
	}
	if runtimes == nil {
		runtimes = fixture(t, "simctl/runtimes.json")
	}
	if devices == nil {
		devices = fixture(t, "devicectl/list-devices.json")
	}
	f := &xcode.FakeRunner{}
	f.Respond(xcode.FakeResult{Stdout: simDevices}, "xcrun", "simctl", "list", "devices", "available", "-j")
	f.Respond(xcode.FakeResult{Stdout: runtimes}, "xcrun", "simctl", "list", "runtimes", "-j")
	f.Respond(xcode.FakeResult{Stdout: devices}, "xcrun", "devicectl", "list", "devices", "--json-output", "-")
	return xcode.New(f), f
}

func ptr(s string) *string { return &s }

func resolve(t *testing.T, req Request, pick ui.Picker) (Destination, error) {
	t.Helper()
	c, _ := client(t, nil, nil, nil)
	return Resolve(context.Background(), c, req, pick)
}

func TestSimulatorMatching(t *testing.T) {
	for _, tc := range []struct {
		query, want string
	}{
		{"00000000-0000-4000-8000-000000000005", "iPhone 17"},                    // exact UDID
		{"00000000-0000-4000-8000-00000000000c", "Apple TV 4K (4th generation)"}, // UDID, any case
		{"iPhone 17", "iPhone 17"},                                               // exact name beats the "iPhone 17e" prefix
		{"iphone air", "iPhone Air"},                                             // case-insensitive
		{"iPad mini", "iPad mini (A20 Pro)"},                                     // unique prefix
	} {
		d, err := resolve(t, Request{Sim: ptr(tc.query)}, nil)
		if err != nil {
			t.Errorf("%q: %v", tc.query, err)
			continue
		}
		if d.Simulator == nil || d.Simulator.Name != tc.want {
			t.Errorf("%q matched %+v, want %s", tc.query, d.Simulator, tc.want)
		}
		if d.Spec != xcode.SimulatorDestination(*d.Simulator) {
			t.Errorf("%q: spec = %q", tc.query, d.Spec)
		}
	}
}

func TestSimulatorAmbiguous(t *testing.T) {
	// "iPhone 18" prefixes both "iPhone 18 Pro" and "iPhone 18 Pro Max".
	_, err := resolve(t, Request{Sim: ptr("iPhone 18")}, nil)
	if err == nil || !strings.Contains(err.Error(), `"iPhone 18" matches 2 simulators`) ||
		!strings.Contains(err.Error(), "00000000-0000-4000-8000-000000000002") || !strings.Contains(err.Error(), "--sim <UDID>") {
		t.Errorf("err = %v", err)
	}

	var labels []string
	pick := func(title string, options []string) (int, error) {
		labels = options
		return 1, nil
	}
	d, err := resolve(t, Request{Sim: ptr("iPhone 18")}, pick)
	if err != nil {
		t.Fatal(err)
	}
	if d.Simulator.Name != "iPhone 18 Pro Max" || len(labels) != 2 {
		t.Errorf("picked %s from %q", d.Simulator.Name, labels)
	}
}

func TestSimulatorSameNameTwoRuntimes(t *testing.T) {
	sims := []byte(`{"devices": {
		"com.apple.CoreSimulator.SimRuntime.iOS-27-2": [{"udid": "NEW", "name": "iPhone 17", "state": "Shutdown", "isAvailable": true}],
		"com.apple.CoreSimulator.SimRuntime.iOS-26-4": [{"udid": "OLD", "name": "iPhone 17", "state": "Shutdown", "isAvailable": true}]
	}}`)
	c, _ := client(t, sims, []byte(`{"runtimes": []}`), nil)
	_, err := Resolve(context.Background(), c, Request{Sim: ptr("iPhone 17")}, nil)
	if err == nil || !strings.Contains(err.Error(), "iOS 27.2") || !strings.Contains(err.Error(), "iOS 26.4") {
		t.Errorf("err = %v", err)
	}
}

func TestSimulatorNoMatch(t *testing.T) {
	_, err := resolve(t, Request{Sim: ptr("Nokia")}, nil)
	if err == nil || !strings.Contains(err.Error(), "runway simulators") {
		t.Errorf("err = %v", err)
	}
}

func TestDefaultSimulator(t *testing.T) {
	// The fixture has a booted iPad but no booted iPhone: first iPhone wins.
	d, err := resolve(t, Request{Sim: ptr("")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Simulator.Name != "iPhone 17" {
		t.Errorf("default = %s, want iPhone 17", d.Simulator.Name)
	}

	sims := []byte(`{"devices": {
		"com.apple.CoreSimulator.SimRuntime.iOS-27-2": [
			{"udid": "A", "name": "iPhone 17", "state": "Shutdown", "isAvailable": true},
			{"udid": "B", "name": "iPhone 18 Pro", "state": "Booted", "isAvailable": true}
		],
		"com.apple.CoreSimulator.SimRuntime.iOS-26-4": [{"udid": "C", "name": "iPhone 16", "state": "Booted", "isAvailable": true}]
	}}`)
	c, _ := client(t, sims, []byte(`{"runtimes": []}`), nil)
	d, err = Resolve(context.Background(), c, Request{}, nil) // nothing specified, not interactive
	if err != nil {
		t.Fatal(err)
	}
	if d.Simulator.UDID != "B" {
		t.Errorf("default = %+v, want the booted iPhone on the newest runtime", d.Simulator)
	}

	tvOnly := []byte(`{"devices": {"com.apple.CoreSimulator.SimRuntime.tvOS-27-2": [{"udid": "T", "name": "Apple TV", "state": "Shutdown", "isAvailable": true}]}}`)
	c, _ = client(t, tvOnly, []byte(`{"runtimes": []}`), nil)
	if _, err := Resolve(context.Background(), c, Request{}, nil); err == nil || !strings.Contains(err.Error(), "no iOS simulator") {
		t.Errorf("tvOS only: err = %v", err)
	}
}

func TestDevice(t *testing.T) {
	d, err := resolve(t, Request{Device: ptr("")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Device == nil || d.Device.Name != "iPad van Tester" || d.Spec != "platform=iOS,id=00008103-0000000000000008" {
		t.Errorf("default device = %+v (%s), want the wired iPad", d.Device, d.Spec)
	}
	if got := d.String(); got != "iPad van Tester (iOS 27.2, wired)" {
		t.Errorf("String() = %q", got)
	}

	for _, tc := range []struct{ query, want string }{
		{"iphone", "iPhone 13 van Tester"},
		{"Living Room", "Living Room"},
		{"00008110-000000000000000a", "iPhone 13 van Tester"},
	} {
		d, err := resolve(t, Request{Device: ptr(tc.query)}, nil)
		if err != nil || d.Device.Name != tc.want {
			t.Errorf("%q: %+v, %v", tc.query, d.Device, err)
		}
	}
}

func TestDeviceErrors(t *testing.T) {
	// The Watch's name contains a non-breaking space; a normal space must match it.
	_, err := resolve(t, Request{Device: ptr("Apple Watch")}, nil)
	if err == nil || !strings.Contains(err.Error(), "is not reachable") {
		t.Errorf("unavailable device: err = %v", err)
	}
	if _, err := resolve(t, Request{Device: ptr("Pixel")}, nil); err == nil || !strings.Contains(err.Error(), "runway devices --all") {
		t.Errorf("no match: err = %v", err)
	}

	c, _ := client(t, nil, nil, []byte(`{"info": {"outcome": "success"}, "result": {"devices": []}}`))
	if _, err := Resolve(context.Background(), c, Request{Device: ptr("")}, nil); err == nil || !strings.Contains(err.Error(), "no devices found") {
		t.Errorf("none known: err = %v", err)
	}

	unpaired := []byte(`{"info": {"outcome": "success"}, "result": {"devices": [
		{"properties": {"hardware": {"udid": "U1", "platform": "iOS"}, "connection": {"pairingState": "unpaired", "transportType": "wired"}, "state": {"name": "New Phone"}}}
	]}}`)
	c, _ = client(t, nil, nil, unpaired)
	if _, err := Resolve(context.Background(), c, Request{Device: ptr("New")}, nil); err == nil || !strings.Contains(err.Error(), "not paired") {
		t.Errorf("unpaired: err = %v", err)
	}
	if _, err := Resolve(context.Background(), c, Request{Device: ptr("")}, nil); err == nil || !strings.Contains(err.Error(), "lists 1 known") {
		t.Errorf("none available: err = %v", err)
	}
}

func TestConfigured(t *testing.T) {
	for _, tc := range []struct {
		configured, want string
	}{
		{"sim:iPhone Air", "iPhone Air"},
		{"simulator: iPad mini", "iPad mini (A20 Pro)"},
		{"sim", "iPhone 17"},
		{"device", "iPad van Tester"},
		{"device:iPhone", "iPhone 13 van Tester"},
	} {
		d, err := resolve(t, Request{Configured: tc.configured, Source: ".runway.toml"}, nil)
		if err != nil {
			t.Errorf("%q: %v", tc.configured, err)
			continue
		}
		var got string
		if d.Simulator != nil {
			got = d.Simulator.Name
		} else {
			got = d.Device.Name
		}
		if got != tc.want {
			t.Errorf("%q → %s, want %s", tc.configured, got, tc.want)
		}
	}

	_, err := resolve(t, Request{Configured: "phone:X", Source: "RUNWAY_DESTINATION"}, nil)
	if err == nil || !strings.Contains(err.Error(), `invalid destination "phone:X" (from RUNWAY_DESTINATION)`) {
		t.Errorf("invalid: err = %v", err)
	}
}

func TestFlagsBeatConfigured(t *testing.T) {
	d, err := resolve(t, Request{Sim: ptr("iPhone Air"), Configured: "device"}, nil)
	if err != nil || d.Simulator == nil || d.Simulator.Name != "iPhone Air" {
		t.Errorf("got %+v, %v", d, err)
	}
	d, err = resolve(t, Request{Raw: "platform=macOS", Sim: ptr("x"), Configured: "device"}, nil)
	if err != nil || d.Spec != "platform=macOS" || d.Simulator != nil || d.String() != "platform=macOS" {
		t.Errorf("raw: %+v, %v", d, err)
	}
}

func TestPickAny(t *testing.T) {
	var labels []string
	pick := func(title string, options []string) (int, error) {
		labels = options
		return len(options) - 1, nil // the last one: a device
	}
	d, err := resolve(t, Request{}, pick)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 14+3 {
		t.Fatalf("got %d choices, want 14 simulators + 3 available devices: %q", len(labels), labels)
	}
	if labels[0] != "iPad (A16) — iOS 27.2 Simulator (booted)" {
		t.Errorf("first label = %q", labels[0])
	}
	if d.Device == nil || d.Device.Name != "iPhone 13 van Tester" {
		t.Errorf("picked %+v", d)
	}

	cancel := func(string, []string) (int, error) { return -1, ui.ErrCancelled }
	if _, err := resolve(t, Request{}, cancel); !errors.Is(err, ui.ErrCancelled) {
		t.Errorf("cancel: err = %v", err)
	}
}

func TestPickAnyIgnoresDevicectlFailure(t *testing.T) {
	c, f := client(t, nil, nil, nil)
	f.Respond(xcode.FakeResult{Err: errors.New("devicectl broke")}, "xcrun", "devicectl", "list", "devices", "--json-output", "-")
	n := 0
	pick := func(_ string, options []string) (int, error) { n = len(options); return 0, nil }
	if _, err := Resolve(context.Background(), c, Request{}, pick); err != nil {
		t.Fatal(err)
	}
	if n != 14 {
		t.Errorf("choices = %d, want the 14 simulators", n)
	}
}
