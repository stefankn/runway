package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMergePrecedence(t *testing.T) {
	flags := Layer{SourceFlag, Config{Scheme: "FlagScheme"}, "/cwd"}
	env := Layer{SourceEnv, Config{Scheme: "EnvScheme", Configuration: "Release", Team: "ENVTEAM"}, "/cwd"}
	file := Layer{SourceFile, Config{Scheme: "FileScheme", Configuration: "Beta", Team: "FILETEAM", LogSubsystem: "com.example"}, "/proj"}

	r, err := Merge("/proj", "/proj/.runway.toml", flags, env, file)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Scheme:        "FlagScheme",
		Configuration: "Release",
		Team:          "ENVTEAM",
		LogSubsystem:  "com.example",
		DerivedData:   "/proj/.build/runway",
	}
	if r.Config != want {
		t.Errorf("config:\n got %+v\nwant %+v", r.Config, want)
	}
	wantSources := map[string]Source{
		KeyScheme:        SourceFlag,
		KeyConfiguration: SourceEnv,
		KeyTeam:          SourceEnv,
		KeyLogSubsystem:  SourceFile,
		KeyDerivedData:   SourceDefault,
	}
	if !reflect.DeepEqual(r.Sources, wantSources) {
		t.Errorf("sources = %v, want %v", r.Sources, wantSources)
	}
	if r.File != "/proj/.runway.toml" || r.Root != "/proj" {
		t.Errorf("file/root = %q, %q", r.File, r.Root)
	}
}

func TestMergeDefaults(t *testing.T) {
	r, err := Merge("/proj", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Configuration != "Debug" || r.Sources[KeyConfiguration] != SourceDefault {
		t.Errorf("configuration = %q (%s)", r.Configuration, r.Sources[KeyConfiguration])
	}
	if r.DerivedData != "/proj/.build/runway" {
		t.Errorf("derived_data = %q", r.DerivedData)
	}
}

func TestMergeContainerIsOneSetting(t *testing.T) {
	flags := Layer{SourceFlag, Config{Project: "Other.xcodeproj"}, "/cwd"}
	file := Layer{SourceFile, Config{Workspace: "App.xcworkspace"}, "/proj"}
	r, err := Merge("/proj", "", flags, file)
	if err != nil {
		t.Fatal(err)
	}
	if r.Project != "/cwd/Other.xcodeproj" || r.Workspace != "" {
		t.Errorf("project = %q, workspace = %q", r.Project, r.Workspace)
	}
	if _, ok := r.Sources[KeyWorkspace]; ok {
		t.Error("workspace has a source although it was overridden")
	}

	both := Layer{SourceFile, Config{Workspace: "A.xcworkspace", Project: "B.xcodeproj"}, "/proj"}
	if _, err := Merge("/proj", "", both); err == nil || !strings.Contains(err.Error(), "both workspace and project") {
		t.Errorf("err = %v", err)
	}
}

func TestMergePaths(t *testing.T) {
	home, _ := os.UserHomeDir()
	file := Layer{SourceFile, Config{Workspace: "ios/App.xcworkspace", DerivedData: "build"}, "/proj"}
	r, _ := Merge("/proj", "", file)
	if r.Workspace != "/proj/ios/App.xcworkspace" || r.DerivedData != "/proj/build" {
		t.Errorf("file-relative paths: %q, %q", r.Workspace, r.DerivedData)
	}

	env := Layer{SourceEnv, Config{DerivedData: "~/dd", Project: "/abs/../abs/P.xcodeproj"}, "/cwd"}
	r, _ = Merge("/proj", "", env)
	if r.DerivedData != filepath.Join(home, "dd") || r.Project != "/abs/P.xcodeproj" {
		t.Errorf("env paths: %q, %q", r.DerivedData, r.Project)
	}
}

func TestFindFile(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindFile(deep); got != "" {
		// A .runway.toml above the temp dir would be found; that's not this test's concern.
		if strings.HasPrefix(got, root) {
			t.Errorf("found %q before creating one", got)
		}
	}
	want := filepath.Join(root, "a", FileName)
	if err := os.WriteFile(want, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindFile(deep); got != want {
		t.Errorf("FindFile = %q, want %q", got, want)
	}
	// A directory with the same name is not a config file.
	if err := os.Mkdir(filepath.Join(root, "a", "b", FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindFile(deep); got != want {
		t.Errorf("FindFile with a directory in the way = %q, want %q", got, want)
	}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFile(t *testing.T) {
	p := writeFile(t, `
scheme        = "MyApp"
workspace     = "MyApp.xcworkspace"
configuration = "Debug"
team          = "ABCDE12345"
destination   = "sim:iPhone 18 Pro"
derived_data  = ".build/runway"
log_subsystem = "com.example.MyApp"
`)
	c, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Scheme: "MyApp", Workspace: "MyApp.xcworkspace", Configuration: "Debug", Team: "ABCDE12345",
		Destination: "sim:iPhone 18 Pro", DerivedData: ".build/runway", LogSubsystem: "com.example.MyApp",
	}
	if c != want {
		t.Errorf("got %+v\nwant %+v", c, want)
	}
}

func TestLoadFileErrors(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"unknown key", `schem = "MyApp"`, `unknown key "schem" (valid keys: scheme, workspace`},
		{"invalid TOML", `scheme = MyApp`, "line 1"},
		{"wrong type", `scheme = 1`, "scheme"},
	} {
		_, err := LoadFile(writeFile(t, tc.content))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

func TestFromEnv(t *testing.T) {
	env := map[string]string{
		"RUNWAY_SCHEME":        "S",
		"RUNWAY_PROJECT":       "P.xcodeproj",
		"RUNWAY_DERIVED_DATA":  "dd",
		"RUNWAY_LOG_SUBSYSTEM": "com.x",
		"SCHEME":               "ignored",
	}
	c := FromEnv(func(k string) string { return env[k] })
	want := Config{Scheme: "S", Project: "P.xcodeproj", DerivedData: "dd", LogSubsystem: "com.x"}
	if c != want {
		t.Errorf("got %+v, want %+v", c, want)
	}
}

func TestKeysAndGet(t *testing.T) {
	keys := Keys()
	if len(keys) != 8 || keys[0] != KeyScheme || keys[7] != KeyLogSubsystem {
		t.Errorf("keys = %v", keys)
	}
	c := Config{Team: "T"}
	if c.Get(KeyTeam) != "T" || c.Get(KeyScheme) != "" {
		t.Error("Get returned wrong values")
	}
}

func TestDescribe(t *testing.T) {
	r := Resolved{File: "/p/.runway.toml", Sources: map[string]Source{
		KeyDerivedData: SourceFlag, KeyTeam: SourceEnv, KeyScheme: SourceFile, KeyConfiguration: SourceDefault,
	}}
	for key, want := range map[string]string{
		KeyDerivedData:   "--derived-data",
		KeyTeam:          "RUNWAY_TEAM",
		KeyScheme:        "/p/.runway.toml",
		KeyConfiguration: "default",
	} {
		if got := r.Describe(key); got != want {
			t.Errorf("Describe(%s) = %q, want %q", key, got, want)
		}
	}
}
