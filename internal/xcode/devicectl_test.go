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

func TestDeviceCommands(t *testing.T) {
	ctx := context.Background()
	f := &FakeRunner{Default: FakeResult{Stdout: readFixture(t, "devicectl/processes.json")}}
	c := New(f)
	const udid = "00008103-000E64123663401E"

	if err := c.DeviceInstall(ctx, udid, "/b/My App.app"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"-flag", "two words", "--"}} {
		p, err := c.DeviceLaunch(ctx, udid, "com.x.app", args, []string{"FOO=bar baz"}, ConsoleIO{})
		if err != nil {
			t.Fatal(err)
		}
		_ = p.Wait()
	}
	pid, found, err := c.DeviceAppPID(ctx, udid, AppProduct{Path: "/b/ActorTest.app", Executable: "ActorTest"})
	if err != nil || !found || pid != 5103 {
		t.Errorf("DeviceAppPID = %d, %v, %v; want 5103", pid, found, err)
	}
	if err := c.DeviceTerminate(ctx, udid, 5103); err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, cmd := range f.Calls() {
		got = append(got, cmd.String())
	}
	want := []string{
		"xcrun devicectl device install app --device 00008103-000E64123663401E '/b/My App.app'",
		"DEVICECTL_CHILD_FOO='bar baz' xcrun devicectl device process launch --console --terminate-existing --device 00008103-000E64123663401E com.x.app",
		// The app's own "--" is passed along after devicectl's.
		"DEVICECTL_CHILD_FOO='bar baz' xcrun devicectl device process launch --console --terminate-existing --device 00008103-000E64123663401E com.x.app -- -flag 'two words' --",
		"xcrun devicectl device info processes --device 00008103-000E64123663401E --json-output -",
		"xcrun devicectl device process terminate --device 00008103-000E64123663401E --pid 5103",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n got %q\nwant %q", got, want)
	}
}

// The fixture is a real `devicectl device info processes` capture, trimmed;
// the ActorTest entries follow the path pattern of the real installed apps.
func TestFindAppProcess(t *testing.T) {
	data := readFixture(t, "devicectl/processes.json")
	for _, tc := range []struct {
		wrapper, executable string
		pid                 int
		found               bool
	}{
		{"ActorTest.app", "ActorTest", 5103, true},   // not the widget extension or the helper
		{"MobileCal.app", "MobileCal", 585, true},    // a real installed app
		{"SpringBoard.app", "SpringBoard", 0, false}, // system app, not under /Bundle/Application/
		{"Other.app", "Other", 0, false},
	} {
		pid, found, err := findAppProcess(data, tc.wrapper, tc.executable)
		if err != nil || pid != tc.pid || found != tc.found {
			t.Errorf("%s: got %d, %v, %v; want %d, %v", tc.wrapper, pid, found, err, tc.pid, tc.found)
		}
	}
	if _, _, err := findAppProcess([]byte("nope"), "A.app", "A"); err == nil {
		t.Error("invalid JSON: want error")
	}
}

func TestDeviceWrappersWrapErrors(t *testing.T) {
	ctx := context.Background()
	fail := &ExitError{Cmd: Cmd{Name: "xcrun"}, Code: 1, Stderr: []byte("ERROR: boom")}
	c := New(&FakeRunner{Default: FakeResult{Err: fail, StartErr: errors.New("no devicectl")}})
	for _, tc := range []struct {
		err  error
		want string
	}{
		{c.DeviceInstall(ctx, "D", "/a.app"), "installing app: "},
		{c.DeviceTerminate(ctx, "D", 1), "stopping app: "},
	} {
		if tc.err == nil || !strings.HasPrefix(tc.err.Error(), tc.want) {
			t.Errorf("err = %v, want prefix %q", tc.err, tc.want)
		}
	}
	if _, err := c.DeviceLaunch(ctx, "D", "x", nil, nil, ConsoleIO{}); err == nil || !strings.HasPrefix(err.Error(), "launching app: ") {
		t.Errorf("launch err = %v", err)
	}
	if _, _, err := c.DeviceAppPID(ctx, "D", AppProduct{}); err == nil || !strings.HasPrefix(err.Error(), "listing device processes: ") {
		t.Errorf("pid err = %v", err)
	}
}
