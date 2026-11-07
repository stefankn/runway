package xcode

import (
	"context"
	"encoding/json"
	"fmt"
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
