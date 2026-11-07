package xcode

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// ListDevices returns the physical devices CoreDevice knows about (paired or
// not, connected or not), sorted with SortDevices. Simulators, which devicectl
// also lists, are left out.
//
// devicectl writes only JSON to stdout with `--json-output -`; its
// human-readable table goes to stderr, which is captured and ignored.
func (c *Client) ListDevices(ctx context.Context) ([]Device, error) {
	out, err := c.r.Output(ctx, xcrun("devicectl", "list", "devices", "--json-output", "-"))
	if err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}
	return parseDevices(out)
}

// Output of `devicectl list devices --json-output -`. Only the fields runway
// uses are decoded, because several others change type between JSON versions.
type rawDeviceList struct {
	Info struct {
		Outcome     string `json:"outcome"`
		JSONVersion int    `json:"jsonVersion"`
	} `json:"info"`
	Result struct {
		Devices []rawDevice `json:"devices"`
	} `json:"result"`
}

type rawDevice struct {
	// Current format (Xcode 27, JSON version 5).
	Properties *struct {
		Connection struct {
			PairingState  string `json:"pairingState"`
			State         string `json:"state"`
			TransportType string `json:"transportType"`
		} `json:"connection"`
		Hardware rawHardware `json:"hardware"`
		Software struct {
			OSVersionNumber json.RawMessage `json:"osVersionNumber"`
		} `json:"software"`
		State struct {
			Name                string          `json:"name"`
			DeveloperModeStatus json.RawMessage `json:"developerModeStatus"`
		} `json:"state"`
	} `json:"properties"`

	// Deprecated in JSON version 5, the only format before Xcode 27.
	HardwareProperties rawHardware `json:"hardwareProperties"`
	DeviceProperties   struct {
		Name                string          `json:"name"`
		OSVersionNumber     json.RawMessage `json:"osVersionNumber"`
		DeveloperModeStatus json.RawMessage `json:"developerModeStatus"`
	} `json:"deviceProperties"`
	ConnectionProperties struct {
		PairingState  string `json:"pairingState"`
		TransportType string `json:"transportType"`
		TunnelState   string `json:"tunnelState"`
	} `json:"connectionProperties"`
}

type rawHardware struct {
	UDID          string `json:"udid"`
	MarketingName string `json:"marketingName"`
	ProductType   string `json:"productType"`
	Platform      string `json:"platform"`
	Reality       string `json:"reality"` // "physical", "simulated"; often absent for physical devices
}

func parseDevices(data []byte) ([]Device, error) {
	var list rawDeviceList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parsing devicectl JSON: %w", err)
	}
	if list.Info.Outcome != "success" {
		return nil, fmt.Errorf("devicectl reported outcome %q", list.Info.Outcome)
	}
	devs := []Device{}
	for _, raw := range list.Result.Devices {
		d, physical := deviceFromJSON(raw)
		if physical && d.UDID != "" {
			devs = append(devs, d)
		}
	}
	SortDevices(devs)
	return devs, nil
}

// deviceFromJSON is the single place that knows devicectl's device format.
// Each field is read from the `properties` dictionary first and falls back to
// the deprecated `hardwareProperties` / `deviceProperties` /
// `connectionProperties`. physical is false for simulators.
//
// Mapping verified against Xcode 27.2 (devicectl 651.32, jsonVersion 5) on 2026-11-07:
//
//	UDID          properties.hardware.udid                  hardwareProperties.udid
//	              (the top-level "identifier" is a CoreDevice UUID, not the UDID)
//	Name          properties.state.name                     deviceProperties.name
//	Model         properties.hardware.marketingName,        same keys in hardwareProperties
//	              else .productType
//	OSVersion     properties.software.osVersionNumber       deviceProperties.osVersionNumber
//	              {"stringValue":"27.2",…}                  "27.2"
//	Platform      properties.hardware.platform              hardwareProperties.platform
//	Paired        properties.connection.pairingState        connectionProperties.pairingState
//	DeveloperMode properties.state.developerModeStatus      deviceProperties.developerModeStatus
//	              {"enabled":{"mode":1}}                    "enabled"
//	Connection    properties.connection.transportType       connectionProperties.transportType
//	              ("localNetwork", "wired"), and .state      and .tunnelState
//	              ("unavailable" when out of reach)
//	Simulated     properties.hardware.reality=="simulated"  hardwareProperties.reality
func deviceFromJSON(raw rawDevice) (d Device, physical bool) {
	var (
		hw, oldHW                       = rawHardware{}, raw.HardwareProperties
		name, pairing, transport, state string
		osVersion, devMode              json.RawMessage
	)
	if p := raw.Properties; p != nil {
		hw = p.Hardware
		name, osVersion, devMode = p.State.Name, p.Software.OSVersionNumber, p.State.DeveloperModeStatus
		pairing, transport, state = p.Connection.PairingState, p.Connection.TransportType, p.Connection.State
	}
	old := raw.DeviceProperties
	oldConn := raw.ConnectionProperties

	d = Device{
		UDID:          firstNonEmpty(hw.UDID, oldHW.UDID),
		Name:          firstNonEmpty(name, old.Name),
		Model:         firstNonEmpty(hw.MarketingName, oldHW.MarketingName, hw.ProductType, oldHW.ProductType),
		OSVersion:     firstNonEmpty(versionString(osVersion), versionString(old.OSVersionNumber)),
		Platform:      firstNonEmpty(hw.Platform, oldHW.Platform),
		Paired:        firstNonEmpty(pairing, oldConn.PairingState) == "paired",
		DeveloperMode: developerModeEnabled(firstNonEmptyRaw(devMode, old.DeveloperModeStatus)),
		Connection: connectionKind(
			firstNonEmpty(transport, oldConn.TransportType),
			firstNonEmpty(state, oldConn.TunnelState),
		),
	}
	physical = firstNonEmpty(hw.Reality, oldHW.Reality) != "simulated"
	return d, physical
}

func connectionKind(transport, state string) string {
	if state == "unavailable" {
		return ConnectionUnavailable
	}
	switch transport {
	case "wired":
		return ConnectionWired
	case "localNetwork":
		return ConnectionNetwork
	case "":
		return ConnectionUnavailable
	default:
		return ConnectionNetwork
	}
}

// versionString accepts both a plain string ("27.2") and the newer object form
// ({"stringValue":"27.2",…}).
func versionString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		StringValue string `json:"stringValue"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.StringValue
	}
	return ""
}

// developerModeEnabled accepts both "enabled" and the newer enum-as-object
// form ({"enabled":{…}} / {"disabled":{…}}).
func developerModeEnabled(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s == "enabled"
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		_, ok := obj["enabled"]
		return ok
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptyRaw(vals ...json.RawMessage) json.RawMessage {
	for _, v := range vals {
		if len(v) > 0 && string(v) != "null" {
			return v
		}
	}
	return nil
}

// DeviceInstall installs an app bundle on a physical device.
func (c *Client) DeviceInstall(ctx context.Context, udid, appPath string) error {
	if _, err := c.r.Output(ctx, xcrun("devicectl", "device", "install", "app", "--device", udid, appPath)); err != nil {
		return fmt.Errorf("installing app: %w", err)
	}
	return nil
}

// DeviceLaunch launches an app on a physical device with its console
// attached, replacing a running copy, and streams its output until it exits.
// args are passed to the app; env entries ("KEY=VALUE") are set in its
// environment through devicectl's DEVICECTL_CHILD_ prefix. devicectl forwards
// catchable signals it receives to the app.
func (c *Client) DeviceLaunch(ctx context.Context, udid, bundleID string, args, env []string, cio ConsoleIO) (Process, error) {
	cmdArgs := []string{"devicectl", "device", "process", "launch", "--console", "--terminate-existing", "--device", udid, bundleID}
	if len(args) > 0 {
		// "--" stops devicectl from reading the app's arguments as its own options.
		cmdArgs = append(append(cmdArgs, "--"), args...)
	}
	cmd := xcrun(cmdArgs...)
	for _, kv := range env {
		cmd.Env = append(cmd.Env, "DEVICECTL_CHILD_"+kv)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = cio.Stdin, cio.Stdout, cio.Stderr
	p, err := c.r.Start(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("launching app: %w", err)
	}
	return p, nil
}

// DeviceAppPID finds the process of an installed app on a device. found is
// false when the app isn't running.
func (c *Client) DeviceAppPID(ctx context.Context, udid string, app AppProduct) (pid int, found bool, err error) {
	out, err := c.r.Output(ctx, xcrun("devicectl", "device", "info", "processes", "--device", udid, "--json-output", "-"))
	if err != nil {
		return 0, false, fmt.Errorf("listing device processes: %w", err)
	}
	return findAppProcess(out, filepath.Base(app.Path), app.Executable)
}

// Output of `devicectl device info processes --json-output -`.
type rawProcesses struct {
	Result struct {
		RunningProcesses []struct {
			Executable        string `json:"executable"`
			ProcessIdentifier int    `json:"processIdentifier"`
		} `json:"runningProcesses"`
	} `json:"result"`
}

// findAppProcess returns the PID of the installed app whose executable is
// …/Bundle/Application/<uuid>/<wrapper>/<executable>.
func findAppProcess(data []byte, wrapper, executable string) (int, bool, error) {
	var raw rawProcesses
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, false, fmt.Errorf("parsing devicectl processes JSON: %w", err)
	}
	suffix := "/" + wrapper + "/" + executable
	for _, p := range raw.Result.RunningProcesses {
		if strings.Contains(p.Executable, "/Bundle/Application/") && strings.HasSuffix(p.Executable, suffix) {
			return p.ProcessIdentifier, true, nil
		}
	}
	return 0, false, nil
}

// DeviceTerminate stops a process on a physical device.
func (c *Client) DeviceTerminate(ctx context.Context, udid string, pid int) error {
	if _, err := c.r.Output(ctx, xcrun("devicectl", "device", "process", "terminate", "--device", udid, "--pid", strconv.Itoa(pid))); err != nil {
		return fmt.Errorf("stopping app: %w", err)
	}
	return nil
}
