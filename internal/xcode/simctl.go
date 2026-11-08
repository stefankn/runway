package xcode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// ListSimulators returns all available simulators, sorted with SortSimulators.
func (c *Client) ListSimulators(ctx context.Context) ([]Simulator, error) {
	devices, err := c.r.Output(ctx, xcrun("simctl", "list", "devices", "available", "-j"))
	if err != nil {
		return nil, fmt.Errorf("listing simulators: %w", err)
	}
	runtimes, err := c.r.Output(ctx, xcrun("simctl", "list", "runtimes", "-j"))
	if err != nil {
		return nil, fmt.Errorf("listing simulator runtimes: %w", err)
	}
	return parseSimulators(devices, runtimes)
}

// Output of `simctl list devices available -j`. Only the fields runway uses.
type rawSimDevices struct {
	Devices map[string][]struct {
		UDID        string `json:"udid"`
		Name        string `json:"name"`
		State       string `json:"state"`
		IsAvailable bool   `json:"isAvailable"`
	} `json:"devices"`
}

// Output of `simctl list runtimes -j`.
type rawRuntimes struct {
	Runtimes []rawRuntime `json:"runtimes"`
}

type rawRuntime struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	Version    string `json:"version"`
}

func parseSimulators(devicesJSON, runtimesJSON []byte) ([]Simulator, error) {
	var devs rawSimDevices
	if err := json.Unmarshal(devicesJSON, &devs); err != nil {
		return nil, fmt.Errorf("parsing simctl devices JSON: %w", err)
	}
	var rts rawRuntimes
	if err := json.Unmarshal(runtimesJSON, &rts); err != nil {
		return nil, fmt.Errorf("parsing simctl runtimes JSON: %w", err)
	}
	byID := make(map[string]rawRuntime, len(rts.Runtimes))
	for _, rt := range rts.Runtimes {
		byID[rt.Identifier] = rt
	}

	sims := []Simulator{}
	for _, id := range slices.Sorted(maps.Keys(devs.Devices)) {
		rt, ok := byID[id]
		if !ok {
			rt = runtimeFromID(id)
		}
		for _, d := range devs.Devices[id] {
			if !d.IsAvailable {
				continue
			}
			sims = append(sims, Simulator{
				UDID:      d.UDID,
				Name:      d.Name,
				State:     d.State,
				Runtime:   rt.Name,
				RuntimeID: id,
				OSVersion: rt.Version,
				Platform:  normalizePlatform(rt.Platform),
			})
		}
	}
	SortSimulators(sims)
	return sims, nil
}

// runtimeFromID derives runtime details from an identifier such as
// "com.apple.CoreSimulator.SimRuntime.iOS-27-2" when simctl no longer lists
// the runtime itself.
func runtimeFromID(id string) rawRuntime {
	rt := rawRuntime{Identifier: id, Name: id}
	_, suffix, ok := strings.Cut(id, "SimRuntime.")
	if !ok {
		return rt
	}
	parts := strings.Split(suffix, "-")
	var version []string
	for _, p := range parts[1:] {
		if _, err := strconv.Atoi(p); err != nil {
			break
		}
		version = append(version, p)
	}
	rt.Platform = normalizePlatform(parts[0])
	rt.Version = strings.Join(version, ".")
	rt.Name = strings.TrimSpace(rt.Platform + " " + rt.Version)
	return rt
}

// normalizePlatform maps Apple's internal platform names to the public ones.
func normalizePlatform(p string) string {
	if p == "xrOS" {
		return "visionOS"
	}
	return p
}

// DeviceHubBundleID identifies the app that shows simulator windows. Xcode 27
// replaced Simulator.app with DeviceHub.
const DeviceHubBundleID = "com.apple.dt.Devices"

// SimBoot boots a simulator if needed and waits until it has finished
// booting. It returns at once if the simulator is already booted.
func (c *Client) SimBoot(ctx context.Context, udid string) error {
	if _, err := c.r.Output(ctx, xcrun("simctl", "bootstatus", udid, "-b")); err != nil {
		return fmt.Errorf("booting simulator: %w", err)
	}
	return nil
}

// SimInstall installs an app bundle on a simulator.
func (c *Client) SimInstall(ctx context.Context, udid, appPath string) error {
	if _, err := c.r.Output(ctx, xcrun("simctl", "install", udid, appPath)); err != nil {
		return fmt.Errorf("installing app: %w", err)
	}
	return nil
}

// ConsoleIO connects a launched app's console to runway's terminal.
type ConsoleIO struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// SimLaunch launches an app and streams its stdout and stderr until it exits.
// A running copy is replaced. args are passed to the app; env entries
// ("KEY=VALUE") are set in its environment through simctl's SIMCTL_CHILD_
// prefix. Signals sent to the returned process are passed on to the app.
func (c *Client) SimLaunch(ctx context.Context, udid, bundleID string, args, env []string, cio ConsoleIO) (Process, error) {
	cmd := xcrun(append([]string{"simctl", "launch", "--console-pty", "--terminate-running-process", udid, bundleID}, args...)...)
	for _, kv := range env {
		cmd.Env = append(cmd.Env, "SIMCTL_CHILD_"+kv)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = cio.Stdin, cio.Stdout, cio.Stderr
	p, err := c.r.Start(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("launching app: %w", err)
	}
	return p, nil
}

// SimTerminate stops an app on a simulator.
func (c *Client) SimTerminate(ctx context.Context, udid, bundleID string) error {
	if _, err := c.r.Output(ctx, xcrun("simctl", "terminate", udid, bundleID)); err != nil {
		return fmt.Errorf("stopping app: %w", err)
	}
	return nil
}

// SimScreenshot saves a screenshot of a booted simulator's screen to path.
// format is simctl's --type: png, jpeg, tiff, bmp or gif. Without --type,
// simctl guesses from the extension but writes PNG data for one it doesn't
// know, so it is always passed. An existing file is overwritten.
func (c *Client) SimScreenshot(ctx context.Context, udid, path, format string) error {
	if _, err := c.r.Output(ctx, xcrun("simctl", "io", udid, "screenshot", "--type="+format, path)); err != nil {
		return fmt.Errorf("taking screenshot: %w", err)
	}
	return nil
}

// OpenDeviceHub brings the DeviceHub window (which shows simulators) to the front.
func (c *Client) OpenDeviceHub(ctx context.Context) error {
	if _, err := c.r.Output(ctx, Cmd{Name: "open", Args: []string{"-b", DeviceHubBundleID}}); err != nil {
		return fmt.Errorf("opening DeviceHub: %w", err)
	}
	return nil
}
