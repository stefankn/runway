// Package destination turns flags, configuration or a picker choice into the
// simulator or device to build and run on.
package destination

import (
	"context"
	"fmt"
	"strings"

	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

// Request is what the user asked for. Flags take priority over Configured.
type Request struct {
	Sim, Device *string // nil: flag not given; "": flag given without a value
	Raw         string  // --destination, passed to xcodebuild unchanged
	Configured  string  // "sim", "sim:<name or UDID>", "device" or "device:<name or UDID>"
	Source      string  // where Configured came from, for error messages
}

// Destination is a resolved build/run target. Exactly one of Simulator and
// Device is set, unless the user passed a raw --destination.
type Destination struct {
	Spec      string // xcodebuild -destination value
	Simulator *xcode.Simulator
	Device    *xcode.Device
}

func (d Destination) String() string {
	switch {
	case d.Simulator != nil:
		return fmt.Sprintf("%s (%s Simulator)", d.Simulator.Name, d.Simulator.Runtime)
	case d.Device != nil:
		return fmt.Sprintf("%s (%s, %s)", d.Device.Name, deviceOS(*d.Device), d.Device.Connection)
	default:
		return d.Spec
	}
}

func fromSimulator(s xcode.Simulator) Destination {
	return Destination{Spec: xcode.SimulatorDestination(s), Simulator: &s}
}

func fromDevice(d xcode.Device) Destination {
	return Destination{Spec: xcode.DeviceDestination(d), Device: &d}
}

// Resolve picks the destination. Priority: --destination, --sim/--device, the
// configured destination, the picker (when pick is not nil), and finally the
// default simulator.
func Resolve(ctx context.Context, client *xcode.Client, req Request, pick ui.Picker) (Destination, error) {
	switch {
	case req.Raw != "":
		return Destination{Spec: req.Raw}, nil
	case req.Sim != nil:
		return simulator(ctx, client, *req.Sim, pick)
	case req.Device != nil:
		return device(ctx, client, *req.Device, pick)
	case req.Configured != "":
		kind, query, err := parseConfigured(req.Configured)
		if err != nil {
			return Destination{}, fmt.Errorf("invalid destination %q (from %s); use sim, sim:<name or UDID>, device or device:<name or UDID>",
				req.Configured, req.Source)
		}
		if kind == "sim" {
			return simulator(ctx, client, query, pick)
		}
		return device(ctx, client, query, pick)
	case pick != nil:
		return pickAny(ctx, client, pick)
	default:
		return simulator(ctx, client, "", nil)
	}
}

func parseConfigured(s string) (kind, query string, err error) {
	kind, query, _ = strings.Cut(s, ":")
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "sim", "simulator":
		return "sim", strings.TrimSpace(query), nil
	case "device":
		return "device", strings.TrimSpace(query), nil
	}
	return "", "", fmt.Errorf("unknown destination kind %q", kind)
}

func simulator(ctx context.Context, client *xcode.Client, query string, pick ui.Picker) (Destination, error) {
	sims, err := client.ListSimulators(ctx)
	if err != nil {
		return Destination{}, err
	}
	if len(sims) == 0 {
		return Destination{}, fmt.Errorf("no simulators available; add one in Xcode (Window → Devices and Simulators)")
	}
	if query == "" {
		s, ok := defaultSimulator(sims)
		if !ok {
			return Destination{}, fmt.Errorf("no iOS simulator available; pass --sim <name or UDID> (see `runway simulators`)")
		}
		return fromSimulator(s), nil
	}

	found := match(sims, query, func(s xcode.Simulator) string { return s.Name }, func(s xcode.Simulator) string { return s.UDID })
	switch len(found) {
	case 0:
		return Destination{}, fmt.Errorf("no simulator matches %q; run `runway simulators` to see them", query)
	case 1:
		return fromSimulator(found[0]), nil
	}
	i, err := chooseAmbiguous(query, "simulator", "--sim", pick, len(found), func(i int) []string {
		return []string{found[i].Name, found[i].Runtime, found[i].UDID}
	})
	if err != nil {
		return Destination{}, err
	}
	return fromSimulator(found[i]), nil
}

// defaultSimulator returns a booted iPhone on the newest iOS runtime, else the
// first iPhone on that runtime, else the first simulator on it. sims must be
// sorted with xcode.SortSimulators.
func defaultSimulator(sims []xcode.Simulator) (xcode.Simulator, bool) {
	var newest []xcode.Simulator
	for _, s := range sims {
		if s.Platform != "iOS" {
			continue
		}
		if len(newest) > 0 && xcode.CompareVersions(s.OSVersion, newest[0].OSVersion) != 0 {
			break // sorted newest first
		}
		newest = append(newest, s)
	}
	if len(newest) == 0 {
		return xcode.Simulator{}, false
	}
	isPhone := func(s xcode.Simulator) bool { return strings.HasPrefix(s.Name, "iPhone") }
	for _, s := range newest {
		if isPhone(s) && s.Booted() {
			return s, true
		}
	}
	for _, s := range newest {
		if isPhone(s) {
			return s, true
		}
	}
	return newest[0], true
}

func device(ctx context.Context, client *xcode.Client, query string, pick ui.Picker) (Destination, error) {
	devs, err := client.ListDevices(ctx)
	if err != nil {
		return Destination{}, err
	}
	if query == "" {
		for _, d := range devs {
			if d.Available() {
				return fromDevice(d), nil
			}
		}
		if len(devs) == 0 {
			return Destination{}, fmt.Errorf("no devices found; connect one with a cable, or pair one in Device Hub")
		}
		return Destination{}, fmt.Errorf("no device is connected right now (`runway devices --all` lists %d known); "+
			"connect one with a cable, or unlock it and make sure it's on the same network as this Mac", len(devs))
	}

	found := match(devs, query, func(d xcode.Device) string { return d.Name }, func(d xcode.Device) string { return d.UDID })
	var d xcode.Device
	switch len(found) {
	case 0:
		return Destination{}, fmt.Errorf("no device matches %q; run `runway devices --all` to see them", query)
	case 1:
		d = found[0]
	default:
		i, err := chooseAmbiguous(query, "device", "--device", pick, len(found), func(i int) []string {
			return []string{found[i].Name, found[i].Model, found[i].UDID}
		})
		if err != nil {
			return Destination{}, err
		}
		d = found[i]
	}
	switch {
	case !d.Paired:
		return Destination{}, fmt.Errorf("%s is not paired with this Mac; pair it in Device Hub", d.Name)
	case !d.Available():
		return Destination{}, fmt.Errorf("%s is not reachable; connect it with a cable, or unlock it and make sure it's on the same network as this Mac", d.Name)
	}
	return fromDevice(d), nil
}

// pickAny lets the user choose among available simulators and devices.
func pickAny(ctx context.Context, client *xcode.Client, pick ui.Picker) (Destination, error) {
	sims, err := client.ListSimulators(ctx)
	if err != nil {
		return Destination{}, err
	}
	// A devicectl problem shouldn't prevent picking a simulator.
	devs, _ := client.ListDevices(ctx)

	var (
		labels []string
		dests  []Destination
	)
	for _, s := range sims {
		label := fmt.Sprintf("%s — %s Simulator", s.Name, s.Runtime)
		if s.Booted() {
			label += " (booted)"
		}
		labels = append(labels, label)
		dests = append(dests, fromSimulator(s))
	}
	for _, d := range devs {
		if d.Available() {
			labels = append(labels, fmt.Sprintf("%s — %s, %s, %s", d.Name, d.Model, deviceOS(d), d.Connection))
			dests = append(dests, fromDevice(d))
		}
	}
	if len(dests) == 0 {
		return Destination{}, fmt.Errorf("no simulators or devices available")
	}
	i, err := pick("Choose a destination", labels)
	if err != nil {
		return Destination{}, err
	}
	if i < 0 || i >= len(dests) {
		return Destination{}, fmt.Errorf("invalid destination choice %d", i)
	}
	return dests[i], nil
}

// match finds items by UDID or name: exact UDID, exact name, case-insensitive
// name, then name prefix. It returns the matches of the first stage that has
// any. Non-breaking spaces in names count as spaces.
func match[T any](items []T, query string, name, udid func(T) string) []T {
	q := normalize(query)
	stages := []func(T) bool{
		func(it T) bool { return strings.EqualFold(udid(it), q) },
		func(it T) bool { return normalize(name(it)) == q },
		func(it T) bool { return strings.EqualFold(normalize(name(it)), q) },
		func(it T) bool { return strings.HasPrefix(strings.ToLower(normalize(name(it))), strings.ToLower(q)) },
	}
	for _, stage := range stages {
		var found []T
		for _, it := range items {
			if stage(it) {
				found = append(found, it)
			}
		}
		if len(found) > 0 {
			return found
		}
	}
	return nil
}

func normalize(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, " ", " "))
}

// chooseAmbiguous asks the picker to choose among n matches, or explains the
// ambiguity when runway can't prompt. row(i) returns the cells describing match i.
func chooseAmbiguous(query, what, flag string, pick ui.Picker, n int, row func(int) []string) (int, error) {
	if pick != nil {
		labels := make([]string, n)
		for i := range n {
			labels[i] = strings.Join(row(i), " — ")
		}
		i, err := pick(fmt.Sprintf("%q matches several %ss", query, what), labels)
		if err != nil {
			return -1, err
		}
		if i < 0 || i >= n {
			return -1, fmt.Errorf("invalid %s choice %d", what, i)
		}
		return i, nil
	}
	var b strings.Builder
	t := ui.Table{}
	for i := range n {
		t.Rows = append(t.Rows, ui.Row{Cells: row(i)})
	}
	_ = t.Render(&b)
	return -1, fmt.Errorf("%q matches %d %ss:\n%s\nUse a more specific name, or the UDID: %s <UDID>",
		query, n, what, indent(b.String()), flag)
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

func deviceOS(d xcode.Device) string {
	if d.OSVersion == "" {
		return d.Platform
	}
	return d.Platform + " " + d.OSVersion
}
