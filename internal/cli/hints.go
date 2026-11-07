package cli

import (
	"bytes"
	"strings"
	"sync"
)

// A hint is advice printed after a failed build when its patterns appear in
// xcodebuild's output. It never replaces xcodebuild's own error.
type hint struct {
	patterns []string // case-insensitive substrings
	text     string
}

var buildHints = []hint{
	{
		[]string{"requires a development team", "no account for team", "no profiles for", "no signing certificate"},
		"Set team in .runway.toml or pass --team <TEAM_ID>. Your team ID is in Xcode → Settings → Accounts.",
	},
	{
		[]string{"device is locked", "is locked"},
		"Unlock the device and try again.",
	},
	{
		[]string{"developer mode"},
		"Turn on Developer Mode on the device (Settings → Privacy & Security → Developer Mode), then restart it.",
	},
	{
		[]string{"has not been explicitly trusted", "untrusted developer"},
		"On the device, trust your developer certificate: Settings → General → VPN & Device Management.",
	},
	{
		[]string{"not paired", "pairing"},
		"Pair the device with this Mac in Device Hub.",
	},
	{
		// Xcode 27.2: "Unable to find a device matching the provided destination specifier".
		[]string{"unable to find a device matching", "unable to find a destination matching"},
		"Run `runway simulators` or `runway devices` to see the available destinations.",
	},
}

// maxLine bounds the bytes kept for one unfinished line.
const maxLine = 64 << 10

// hintScanner is an io.Writer that watches build output line by line and
// remembers which hints matched. It is safe for concurrent writes (xcodebuild's
// stdout and stderr are copied in separate goroutines).
type hintScanner struct {
	mu      sync.Mutex
	partial []byte
	matched []bool
}

func newHintScanner() *hintScanner {
	return &hintScanner{matched: make([]bool, len(buildHints))}
}

func (h *hintScanner) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	data := append(h.partial, p...)
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		h.check(string(data[:i]))
		data = data[i+1:]
	}
	if len(data) > maxLine {
		data = data[len(data)-maxLine:]
	}
	h.partial = append(h.partial[:0], data...)
	return len(p), nil
}

func (h *hintScanner) check(line string) {
	lower := strings.ToLower(line)
	for i, hn := range buildHints {
		for _, pat := range hn.patterns {
			if strings.Contains(lower, pat) {
				h.matched[i] = true
				break
			}
		}
	}
}

// Hints returns the matched hints, in a fixed order.
func (h *hintScanner) Hints() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.partial) > 0 {
		h.check(string(h.partial))
		h.partial = h.partial[:0]
	}
	var out []string
	for i, ok := range h.matched {
		if ok {
			out = append(out, buildHints[i].text)
		}
	}
	return out
}
