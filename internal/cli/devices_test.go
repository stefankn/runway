package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stefankn/runway/internal/xcode"
)

func devicesFake(stdout []byte) *xcode.FakeRunner {
	f := &xcode.FakeRunner{}
	f.Respond(xcode.FakeResult{Stdout: stdout}, "xcrun", "devicectl", "list", "devices", "--json-output", "-")
	return f
}

func TestDevicesTable(t *testing.T) {
	out, _, err := runWith(t, devicesFake(fixture(t, "devicectl/list-devices.json")), "devices")
	if err != nil {
		t.Fatal(err)
	}
	want := "NAME                  MODEL                      OS      CONNECTION  UDID\n" +
		"iPad van Tester       iPad Air (5th generation)  27.2    wired       00008103-0000000000000008\n" +
		"Living Room           Apple TV 4K                26.6.1  network     0000000000000000000000000000000000000005\n" +
		"iPhone 13 van Tester  iPhone 13 mini             27.2    network     00008110-000000000000000A\n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestDevicesAll(t *testing.T) {
	out, _, err := runWith(t, devicesFake(fixture(t, "devicectl/list-devices.json")), "devices", "--all")
	if err != nil {
		t.Fatal(err)
	}
	l := lines(out)
	if len(l) != 5 {
		t.Fatalf("got %d lines:\n%s", len(l), out)
	}
	if !strings.HasPrefix(l[4], "Apple Watch van Tester") || !strings.Contains(l[4], "Watch7,16 ") || !strings.Contains(l[4], " -       unavailable  ") {
		t.Errorf("unavailable row = %q", l[4])
	}
}

func TestDevicesJSON(t *testing.T) {
	out, _, err := runWith(t, devicesFake(fixture(t, "devicectl/list-devices.json")), "devices", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var devs []xcode.Device
	if err := json.Unmarshal([]byte(out), &devs); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(devs) != 3 || devs[0].Connection != "wired" || !devs[0].DeveloperMode {
		t.Errorf("devices = %+v", devs)
	}
}

func TestDevicesEmpty(t *testing.T) {
	empty := []byte(`{"info": {"outcome": "success"}, "result": {"devices": []}}`)
	out, errOut, err := runWith(t, devicesFake(empty), "devices")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" || !strings.Contains(errOut, "No devices found") {
		t.Errorf("stdout = %q, stderr = %q", out, errOut)
	}

	onlyUnavailable := []byte(`{"info": {"outcome": "success"}, "result": {"devices": [
		{"properties": {"hardware": {"udid": "W1"}, "connection": {"pairingState": "paired", "state": "unavailable"}, "state": {"name": "Watch"}}}
	]}}`)
	_, errOut, err = runWith(t, devicesFake(onlyUnavailable), "devices")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "Use --all") {
		t.Errorf("stderr = %q", errOut)
	}
}
