package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stefankn/runway/internal/xcode"
)

// projectDir creates a temp folder with the given bundles (directories) and
// files (name → content) and returns its path.
func projectDir(t *testing.T, dirs []string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// listFake answers every xcodebuild -list with the given fixture.
func listFake(t *testing.T, name string) *xcode.FakeRunner {
	return &xcode.FakeRunner{Default: xcode.FakeResult{Stdout: fixture(t, "xcodebuild/"+name)}}
}

// clearEnv makes sure RUNWAY_* variables from the developer's shell don't
// leak into a test.
func clearEnv(t *testing.T) {
	for _, k := range []string{"SCHEME", "WORKSPACE", "PROJECT", "CONFIGURATION", "TEAM", "DESTINATION", "DERIVED_DATA", "LOG_SUBSYSTEM"} {
		t.Setenv("RUNWAY_"+k, "")
	}
}

// row returns the table line for key, with runs of spaces collapsed.
func row(t *testing.T, out, key string) string {
	t.Helper()
	for _, l := range lines(out) {
		if f := strings.Fields(l); len(f) > 0 && f[0] == key {
			return strings.Join(f, " ")
		}
	}
	t.Fatalf("no %q row in:\n%s", key, out)
	return ""
}

func TestConfigDetected(t *testing.T) {
	clearEnv(t)
	dir := projectDir(t, []string{"ActorTest.xcodeproj"}, nil)
	out, _, err := runOpts(t, &options{workDir: dir, testRunner: listFake(t, "list-project.json")}, "config")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Config file: none") {
		t.Errorf("first line: %q", lines(out)[0])
	}
	for key, want := range map[string]string{
		"scheme":        "scheme ActorTest detected",
		"project":       "project ActorTest.xcodeproj detected",
		"workspace":     "workspace -",
		"configuration": "configuration Debug default",
		"derived_data":  "derived_data .build/runway default",
	} {
		if got := row(t, out, key); got != want {
			t.Errorf("%s row = %q, want %q", key, got, want)
		}
	}
}

func TestConfigPrecedence(t *testing.T) {
	clearEnv(t)
	dir := projectDir(t, []string{"ActorTest.xcodeproj"}, map[string]string{
		".runway.toml": "scheme = \"FileScheme\"\nconfiguration = \"Beta\"\nteam = \"FILETEAM\"\nlog_subsystem = \"com.example\"\n",
	})
	sub := filepath.Join(dir, "Sources", "Feature")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNWAY_CONFIGURATION", "Release")
	t.Setenv("RUNWAY_TEAM", "ENVTEAM")

	// Run from a subfolder: the file is found two levels up.
	out, _, err := runOpts(t, &options{workDir: sub, testRunner: &xcode.FakeRunner{}}, "config", "--team", "FLAGTEAM")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Config file: ../../.runway.toml"; lines(out)[0] != want {
		t.Errorf("first line = %q, want %q", lines(out)[0], want)
	}
	for key, want := range map[string]string{
		"scheme":        "scheme FileScheme file",
		"configuration": "configuration Release env",
		"team":          "team FLAGTEAM flag",
		"log_subsystem": "log_subsystem com.example file",
		"project":       "project ../../ActorTest.xcodeproj detected",
	} {
		if got := row(t, out, key); got != want {
			t.Errorf("%s row = %q, want %q", key, got, want)
		}
	}
}

func TestConfigJSON(t *testing.T) {
	clearEnv(t)
	dir := projectDir(t, []string{"App.xcworkspace"}, nil)
	out, _, err := runOpts(t, &options{workDir: dir, testRunner: listFake(t, "list-project.json")}, "config", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got configJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if got.File != "" || got.Root != dir {
		t.Errorf("file/root = %q, %q", got.File, got.Root)
	}
	if ws := got.Settings["workspace"]; ws.Value != filepath.Join(dir, "App.xcworkspace") || ws.Source != "detected" {
		t.Errorf("workspace = %+v", ws)
	}
	if team := got.Settings["team"]; team.Value != "" || team.Source != "" {
		t.Errorf("team = %+v", team)
	}
	if len(got.Settings) != 8 {
		t.Errorf("got %d settings, want 8", len(got.Settings))
	}
}

func TestConfigNoDetect(t *testing.T) {
	clearEnv(t)
	dir := projectDir(t, []string{"ActorTest.xcodeproj"}, nil)
	fake := &xcode.FakeRunner{}
	out, _, err := runOpts(t, &options{workDir: dir, testRunner: fake}, "config", "--no-detect")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("--no-detect ran %d commands", len(fake.Calls()))
	}
	if got := row(t, out, "scheme"); got != "scheme -" {
		t.Errorf("scheme row = %q", got)
	}
}

func TestConfigShowsSettingsThenDetectionError(t *testing.T) {
	clearEnv(t)
	dir := projectDir(t, []string{"Sensors.xcodeproj"}, nil)
	out, _, err := runOpts(t, &options{workDir: dir, testRunner: listFake(t, "list-project-multi.json")}, "config")
	if err == nil || !strings.Contains(err.Error(), "found 3 schemes") {
		t.Fatalf("err = %v", err)
	}
	if got := row(t, out, "project"); got != "project Sensors.xcodeproj detected" {
		t.Errorf("project row = %q", got)
	}
}

func TestConfigPicker(t *testing.T) {
	clearEnv(t)
	dir := projectDir(t, []string{"Sensors.xcodeproj"}, nil)
	var titles []string
	pick := func(title string, options []string) (int, error) {
		titles = append(titles, title)
		return 1, nil
	}
	out, _, err := runOpts(t, &options{workDir: dir, testRunner: listFake(t, "list-project-multi.json"), testPicker: pick}, "config")
	if err != nil {
		t.Fatal(err)
	}
	if got := row(t, out, "scheme"); got != "scheme Sensors detected" {
		t.Errorf("scheme row = %q", got)
	}
	if len(titles) != 1 || titles[0] != "Choose a scheme" {
		t.Errorf("picker titles = %q", titles)
	}
}

func TestConfigErrors(t *testing.T) {
	clearEnv(t)
	for _, tc := range []struct {
		name  string
		files map[string]string
		args  []string
		want  string
	}{
		{"unknown key", map[string]string{".runway.toml": `schem = "X"`}, nil, `unknown key "schem"`},
		{"workspace and project flags", nil, []string{"--workspace", "A.xcworkspace", "--project", "B.xcodeproj"}, "none of the others can be"},
		{"missing configured project", nil, []string{"--project", "Nope.xcodeproj"}, "(from --project) does not exist"},
	} {
		dir := projectDir(t, nil, tc.files)
		_, _, err := runOpts(t, &options{workDir: dir, testRunner: &xcode.FakeRunner{}}, append([]string{"config"}, tc.args...)...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestDisplayPath(t *testing.T) {
	for _, tc := range []struct{ dir, p, want string }{
		{"/a/b", "/a/b/App.xcodeproj", "App.xcodeproj"},
		{"/a/b/c", "/a/b/.runway.toml", "../.runway.toml"},
		{"/a/b/c/d/e/f", "/x.toml", "/x.toml"},
	} {
		if got := displayPath(tc.dir, tc.p); got != tc.want {
			t.Errorf("displayPath(%q, %q) = %q, want %q", tc.dir, tc.p, got, tc.want)
		}
	}
}
