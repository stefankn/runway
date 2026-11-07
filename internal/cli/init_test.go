package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stefankn/runway/internal/config"
	"github.com/stefankn/runway/internal/xcode"
)

func TestInitWritesLoadableFile(t *testing.T) {
	clearEnv(t)
	dir := projectDir(t, []string{"ActorTest.xcodeproj"}, nil)
	out, _, err := runOpts(t, &options{workDir: dir, testRunner: listFake(t, "list-project.json")}, "init")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Wrote .runway.toml (scheme ActorTest, project ActorTest.xcodeproj)\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
	path := filepath.Join(dir, config.FileName)
	c, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("generated file doesn't load: %v", err)
	}
	want := config.Config{Scheme: "ActorTest", Project: "ActorTest.xcodeproj", Configuration: "Debug"}
	if c != want {
		t.Errorf("loaded %+v, want %+v", c, want)
	}
	content, _ := os.ReadFile(path)
	for _, s := range []string{`# team = "ABCDE12345"`, `# destination = "sim:iPhone 18 Pro"`, `# log_subsystem = "com.example.MyApp"`} {
		if !strings.Contains(string(content), s) {
			t.Errorf("file is missing %q:\n%s", s, content)
		}
	}
}

func TestInitWithFlags(t *testing.T) {
	clearEnv(t)
	dir := projectDir(t, []string{"A.xcodeproj", "B.xcodeproj"}, nil)
	fake := &xcode.FakeRunner{}
	_, _, err := runOpts(t, &options{workDir: dir, testRunner: fake},
		"init", "--project", "B.xcodeproj", "--scheme", `My "Quoted" App`, "--team", "TEAM1", "--configuration", "Release", "--derived-data", "out/dd")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("scheme given by flag; xcodebuild should not run")
	}
	c, err := config.LoadFile(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{Scheme: `My "Quoted" App`, Project: "B.xcodeproj", Configuration: "Release", Team: "TEAM1", DerivedData: "out/dd"}
	if c != want {
		t.Errorf("loaded %+v, want %+v", c, want)
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	clearEnv(t)
	dir := projectDir(t, []string{"ActorTest.xcodeproj"}, map[string]string{".runway.toml": "# mine\n"})
	opts := &options{workDir: dir, testRunner: listFake(t, "list-project.json")}

	_, _, err := runOpts(t, opts, "init")
	if err == nil || !strings.Contains(err.Error(), "already exists; use --force") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, config.FileName)); string(b) != "# mine\n" {
		t.Error("existing file was changed")
	}

	if _, _, err := runOpts(t, opts, "init", "--force"); err != nil {
		t.Fatal(err)
	}
	if c, err := config.LoadFile(filepath.Join(dir, config.FileName)); err != nil || c.Scheme != "ActorTest" {
		t.Errorf("after --force: %+v, %v", c, err)
	}
}

func TestInitIgnoresEnvAndParentFile(t *testing.T) {
	clearEnv(t)
	parent := projectDir(t, nil, map[string]string{".runway.toml": "scheme = \"ParentScheme\"\n"})
	dir := filepath.Join(parent, "App")
	if err := os.MkdirAll(filepath.Join(dir, "App.xcodeproj"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNWAY_SCHEME", "EnvScheme")
	_, _, err := runOpts(t, &options{workDir: dir, testRunner: listFake(t, "list-project.json")}, "init")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := config.LoadFile(filepath.Join(dir, config.FileName))
	if c.Scheme != "ActorTest" {
		t.Errorf("scheme = %q, want the detected ActorTest", c.Scheme)
	}
}

func TestTOMLString(t *testing.T) {
	for in, want := range map[string]string{
		"plain":      `"plain"`,
		`a"b\c`:      `"a\"b\\c"`,
		"tab\there":  `"tab\u0009here"`,
		"iPhone Pro": `"iPhone Pro"`,
	} {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}
