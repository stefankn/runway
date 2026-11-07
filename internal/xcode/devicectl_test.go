package xcode

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Physical devices in testdata/devicectl/list-devices.json, captured from
// Xcode 27.2 (devicectl 651.32) with an iPad on USB, an iPhone and Apple TV on
// the network, and an out-of-range Watch. In SortDevices order.
var wantFixtureDevices = []Device{
	{
		UDID: "00008103-0000000000000008", Name: "iPad van Tester", Model: "iPad Air (5th generation)",
		OSVersion: "27.2", Platform: "iOS", Connection: ConnectionWired, Paired: true, DeveloperMode: true,
	},
	{
		UDID: "0000000000000000000000000000000000000005", Name: "Living Room", Model: "Apple TV 4K",
		OSVersion: "26.6.1", Platform: "tvOS", Connection: ConnectionNetwork, Paired: true, DeveloperMode: true,
	},
	{
		UDID: "00008110-000000000000000A", Name: "iPhone 13 van Tester", Model: "iPhone 13 mini",
		OSVersion: "27.2", Platform: "iOS", Connection: ConnectionNetwork, Paired: true, DeveloperMode: true,
	},
	{
		// No marketing name or OS version while unavailable; the name has a U+00A0.
		UDID: "00008310-0000000000000003", Name: "Apple\u00a0Watch van Tester", Model: "Watch7,16",
		OSVersion: "", Platform: "watchOS", Connection: ConnectionUnavailable, Paired: true, DeveloperMode: false,
	},
}

func TestParseDevicesFixture(t *testing.T) {
	devs, err := parseDevices(readFixture(t, "devicectl/list-devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(devs, wantFixtureDevices) {
		t.Errorf("devices:\n got %+v\nwant %+v", devs, wantFixtureDevices)
	}
}

// stripDeviceKeys removes keys from every device in a devicectl JSON document.
func stripDeviceKeys(t *testing.T, data []byte, keys ...string) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, d := range doc["result"].(map[string]any)["devices"].([]any) {
		for _, k := range keys {
			delete(d.(map[string]any), k)
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The same devices must come out whether devicectl sends only the deprecated
// fields (Xcode before 27), only `properties` (a future Xcode), or both.
func TestParseDevicesFormatVariants(t *testing.T) {
	fixture := readFixture(t, "devicectl/list-devices.json")
	for name, data := range map[string][]byte{
		"legacy only":     stripDeviceKeys(t, fixture, "properties"),
		"properties only": stripDeviceKeys(t, fixture, "hardwareProperties", "deviceProperties", "connectionProperties"),
	} {
		devs, err := parseDevices(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(devs, wantFixtureDevices) {
			t.Errorf("%s:\n got %+v\nwant %+v", name, devs, wantFixtureDevices)
		}
	}
}

func TestParseDevicesEdgeCases(t *testing.T) {
	doc := `{"info": {"outcome": "success", "jsonVersion": 5}, "result": {"devices": [
		{"identifier": "core-1", "properties": {
			"hardware": {"udid": "U1", "productType": "iPhone99,1", "platform": "iOS"},
			"connection": {"pairingState": "unpaired", "transportType": "wired", "state": "connected"},
			"state": {"name": "Unpaired", "developerModeStatus": {"disabled": {}}}
		}},
		{"identifier": "core-2", "properties": {
			"hardware": {"platform": "iOS"},
			"state": {"name": "No UDID"}
		}}
	]}}`
	devs, err := parseDevices([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := []Device{{UDID: "U1", Name: "Unpaired", Model: "iPhone99,1", Platform: "iOS", Connection: ConnectionWired}}
	if !reflect.DeepEqual(devs, want) {
		t.Errorf("got %+v\nwant %+v", devs, want)
	}
}

func TestParseDevicesErrors(t *testing.T) {
	for name, doc := range map[string]string{
		"invalid JSON": "devicectl: something went wrong",
		"failure":      `{"info": {"outcome": "failed"}, "result": {}}`,
	} {
		if _, err := parseDevices([]byte(doc)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestListDevicesCommand(t *testing.T) {
	f := &FakeRunner{}
	f.Respond(FakeResult{Stdout: readFixture(t, "devicectl/list-devices.json")}, "xcrun", "devicectl", "list", "devices", "--json-output", "-")
	devs, err := New(f).ListDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != len(wantFixtureDevices) {
		t.Errorf("got %d devices", len(devs))
	}
	calls := f.Calls()
	if len(calls) != 1 || calls[0].String() != "xcrun devicectl list devices --json-output -" {
		t.Errorf("calls = %v", calls)
	}
}

func TestListDevicesError(t *testing.T) {
	f := &FakeRunner{Default: FakeResult{Err: &ExitError{Cmd: xcrun("devicectl"), Code: 1, Stderr: []byte("ERROR: boom")}}}
	_, err := New(f).ListDevices(context.Background())
	var ee *ExitError
	if !errors.As(err, &ee) || !strings.HasPrefix(err.Error(), "listing devices: ") {
		t.Errorf("err = %v", err)
	}
}
