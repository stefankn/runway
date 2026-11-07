package xcode

import (
	"slices"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"27.2", "27.2", 0},
		{"27.10", "27.2", 1},
		{"27.2", "27.10", -1},
		{"27", "27.0", 0},
		{"27.0.1", "27", 1},
		{"18.4", "27.0", -1},
		{"", "", 0},
		{"", "1", -1},
	} {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMatchesVersion(t *testing.T) {
	for _, tc := range []struct {
		version, prefix string
		want            bool
	}{
		{"27.2", "27", true},
		{"27.2", "27.2", true},
		{"27.2.1", "27.2", true},
		{"27.2", "27.2.0", true},
		{"27.20", "27.2", false},
		{"26.4", "27", false},
		{"27.2", "27.3", false},
	} {
		if got := MatchesVersion(tc.version, tc.prefix); got != tc.want {
			t.Errorf("MatchesVersion(%q, %q) = %v, want %v", tc.version, tc.prefix, got, tc.want)
		}
	}
}

func TestSortSimulators(t *testing.T) {
	sims := []Simulator{
		{Name: "Apple TV", Platform: "tvOS", OSVersion: "27.2"},
		{Name: "iPhone B", Platform: "iOS", OSVersion: "26.4"},
		{Name: "Watch", Platform: "watchOS", OSVersion: "27.0"},
		{Name: "Vision", Platform: "visionOS", OSVersion: "27.0"},
		{Name: "iPhone B", Platform: "iOS", OSVersion: "27.10"},
		{Name: "iPhone A", Platform: "iOS", OSVersion: "27.2"},
		{Name: "Mystery", Platform: "fooOS", OSVersion: "1.0"},
	}
	SortSimulators(sims)
	var got []string
	for _, s := range sims {
		got = append(got, s.Platform+" "+s.OSVersion+" "+s.Name)
	}
	want := []string{
		"iOS 27.10 iPhone B",
		"iOS 27.2 iPhone A",
		"iOS 26.4 iPhone B",
		"watchOS 27.0 Watch",
		"tvOS 27.2 Apple TV",
		"visionOS 27.0 Vision",
		"fooOS 1.0 Mystery",
	}
	if !slices.Equal(got, want) {
		t.Errorf("order:\n got %q\nwant %q", got, want)
	}
}

func TestSortDevices(t *testing.T) {
	devs := []Device{
		{Name: "Unpaired", Paired: false, Connection: ConnectionWired},
		{Name: "Gone", Paired: true, Connection: ConnectionUnavailable},
		{Name: "B net", Paired: true, Connection: ConnectionNetwork},
		{Name: "Z wired", Paired: true, Connection: ConnectionWired},
		{Name: "A net", Paired: true, Connection: ConnectionNetwork},
	}
	SortDevices(devs)
	var got []string
	for _, d := range devs {
		got = append(got, d.Name)
	}
	want := []string{"Z wired", "A net", "B net", "Unpaired", "Gone"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %q, want %q", got, want)
	}
}

func TestDeviceAvailable(t *testing.T) {
	for _, tc := range []struct {
		d    Device
		want bool
	}{
		{Device{Paired: true, Connection: ConnectionWired}, true},
		{Device{Paired: true, Connection: ConnectionNetwork}, true},
		{Device{Paired: true, Connection: ConnectionUnavailable}, false},
		{Device{Paired: false, Connection: ConnectionWired}, false},
	} {
		if got := tc.d.Available(); got != tc.want {
			t.Errorf("%+v.Available() = %v, want %v", tc.d, got, tc.want)
		}
	}
}
