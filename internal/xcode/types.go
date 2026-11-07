package xcode

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// Simulator is an available simulator, as reported by simctl.
type Simulator struct {
	UDID      string `json:"udid"`
	Name      string `json:"name"`
	State     string `json:"state"`     // "Booted", "Shutdown", …
	Runtime   string `json:"runtime"`   // "iOS 27.2"
	RuntimeID string `json:"runtimeId"` // "com.apple.CoreSimulator.SimRuntime.iOS-27-2"
	OSVersion string `json:"osVersion"` // "27.2"
	Platform  string `json:"platform"`  // "iOS", "watchOS", "tvOS", "visionOS"
}

// Booted reports whether the simulator is running.
func (s Simulator) Booted() bool { return s.State == "Booted" }

// Device connection kinds.
const (
	ConnectionWired       = "wired"
	ConnectionNetwork     = "network"
	ConnectionUnavailable = "unavailable"
)

// Device is a physical device known to CoreDevice, as reported by devicectl.
type Device struct {
	UDID          string `json:"udid"` // usable with xcodebuild -destination id=…
	Name          string `json:"name"`
	Model         string `json:"model"`     // marketing name, or product type such as "iPhone14,4"
	OSVersion     string `json:"osVersion"` // empty if unknown
	Platform      string `json:"platform"`
	Connection    string `json:"connection"` // ConnectionWired, ConnectionNetwork or ConnectionUnavailable
	Paired        bool   `json:"paired"`
	DeveloperMode bool   `json:"developerMode"`
}

// Available reports whether runway can currently install to the device.
func (d Device) Available() bool { return d.Paired && d.Connection != ConnectionUnavailable }

// CompareVersions compares dotted version strings numerically
// ("27.10" > "27.2"; missing components count as 0). It returns -1, 0 or 1.
func CompareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(as), len(bs)) {
		if c := compareComponent(component(as, i), component(bs, i)); c != 0 {
			return c
		}
	}
	return 0
}

// MatchesVersion reports whether version starts with all components of
// prefix: "27.2.1" matches "27" and "27.2", but "27.20" does not match "27.2".
func MatchesVersion(version, prefix string) bool {
	vs := strings.Split(version, ".")
	for i, p := range strings.Split(prefix, ".") {
		if compareComponent(component(vs, i), p) != 0 {
			return false
		}
	}
	return true
}

func component(parts []string, i int) string {
	if i < len(parts) && parts[i] != "" {
		return parts[i]
	}
	return "0"
}

func compareComponent(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	if aerr == nil && berr == nil {
		return cmp.Compare(an, bn)
	}
	return strings.Compare(a, b)
}

var platformOrder = []string{"iOS", "watchOS", "tvOS", "visionOS"}

func platformRank(p string) int {
	if i := slices.Index(platformOrder, p); i >= 0 {
		return i
	}
	return len(platformOrder)
}

// SortSimulators orders simulators by platform (iOS, watchOS, tvOS, visionOS,
// then others), newest OS first, then name.
func SortSimulators(sims []Simulator) {
	slices.SortStableFunc(sims, func(a, b Simulator) int {
		return cmp.Or(
			cmp.Compare(platformRank(a.Platform), platformRank(b.Platform)),
			strings.Compare(a.Platform, b.Platform),
			CompareVersions(b.OSVersion, a.OSVersion),
			strings.Compare(a.Name, b.Name),
			strings.Compare(a.UDID, b.UDID),
		)
	})
}

var connectionOrder = []string{ConnectionWired, ConnectionNetwork, ConnectionUnavailable}

// SortDevices orders devices available first, wired before network, then by name.
func SortDevices(devs []Device) {
	avail := func(d Device) int {
		if d.Available() {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(devs, func(a, b Device) int {
		return cmp.Or(
			cmp.Compare(avail(a), avail(b)),
			cmp.Compare(slices.Index(connectionOrder, a.Connection), slices.Index(connectionOrder, b.Connection)),
			strings.Compare(a.Name, b.Name),
			strings.Compare(a.UDID, b.UDID),
		)
	})
}
