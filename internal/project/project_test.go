package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stefankn/runway/internal/config"
	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

// mkdirs creates directories (bundles) under a new temp dir and returns it.
func mkdirs(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../xcode/testdata/xcodebuild/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func resolved(t *testing.T, root string, layers ...config.Layer) *config.Resolved {
	t.Helper()
	r, err := config.Merge(root, "", layers...)
	if err != nil {
		t.Fatal(err)
	}
	return &r
}

// pickIndex returns a Picker that records its calls and always chooses i.
func pickIndex(i int, calls *[]string) ui.Picker {
	return func(title string, options []string) (int, error) {
		*calls = append(*calls, title+": "+strings.Join(options, ", "))
		return i, nil
	}
}

func TestDetect(t *testing.T) {
	for _, tc := range []struct {
		name  string
		dirs  []string
		files []string
		want  []string
	}{
		{"workspace beats project", []string{"App.xcodeproj/project.xcworkspace", "App.xcworkspace"}, nil, []string{"workspace:App.xcworkspace"}},
		{"projects sorted", []string{"B.xcodeproj", "A.xcodeproj"}, nil, []string{"project:A.xcodeproj", "project:B.xcodeproj"}},
		{"hidden and non-bundles skipped", []string{".Hidden.xcworkspace", "App.xcodeproj", "Sources"}, []string{"Notes.xcworkspace"}, []string{"project:App.xcodeproj"}},
		{"nothing", []string{"Sources"}, nil, nil},
	} {
		root := mkdirs(t, tc.dirs...)
		for _, f := range tc.files {
			if err := os.WriteFile(filepath.Join(root, f), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		found, err := Detect(root)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, c := range found {
			got = append(got, string(c.Kind)+":"+filepath.Base(c.Path))
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestResolveDetectsSingle(t *testing.T) {
	root := mkdirs(t, "ActorTest.xcodeproj")
	f := &xcode.FakeRunner{Default: xcode.FakeResult{Stdout: fixture(t, "list-project.json")}}
	cfg := resolved(t, root)

	p, err := Resolve(context.Background(), xcode.New(f), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Project{
		Container:     xcode.Container{Kind: xcode.Project, Path: filepath.Join(root, "ActorTest.xcodeproj")},
		Scheme:        "ActorTest",
		Configuration: "Debug",
		DerivedData:   filepath.Join(root, ".build", "runway"),
		Root:          root,
	}
	if p != want {
		t.Errorf("got %+v\nwant %+v", p, want)
	}
	if cfg.Sources[config.KeyProject] != config.SourceDetected || cfg.Sources[config.KeyScheme] != config.SourceDetected {
		t.Errorf("sources = %v", cfg.Sources)
	}
}

func TestResolvePicksWhenSeveral(t *testing.T) {
	root := mkdirs(t, "A.xcworkspace", "B.xcworkspace")
	f := &xcode.FakeRunner{Default: xcode.FakeResult{Stdout: fixture(t, "list-workspace.json")}}
	var calls []string

	p, err := Resolve(context.Background(), xcode.New(f), resolved(t, root), pickIndex(1, &calls))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p.Container.Path) != "B.xcworkspace" || p.Scheme != "MenuBarPlugin" {
		t.Errorf("got %+v", p)
	}
	want := []string{
		"Choose a workspace: A.xcworkspace, B.xcworkspace",
		"Choose a scheme: ActorTest, MenuBarPlugin, Sensors, SensorsWidgetExtension",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("picker calls = %q", calls)
	}
}

func TestResolveSeveralWithoutPicker(t *testing.T) {
	root := mkdirs(t, "A.xcworkspace", "B.xcworkspace")
	_, err := ResolveContainer(resolved(t, root), nil)
	if err == nil || !strings.Contains(err.Error(), "found 2 workspaces, choose one:\n  A.xcworkspace\n  B.xcworkspace\nSet workspace in .runway.toml, or pass --workspace.") {
		t.Errorf("err = %v", err)
	}

	f := &xcode.FakeRunner{Default: xcode.FakeResult{Stdout: fixture(t, "list-project-multi.json")}}
	_, err = ResolveScheme(context.Background(), xcode.New(f), resolved(t, root), xcode.Container{}, nil)
	if err == nil || !strings.Contains(err.Error(), "found 3 schemes") || !strings.Contains(err.Error(), "--scheme") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveConfigured(t *testing.T) {
	root := mkdirs(t, "Other.xcodeproj", "App.xcworkspace")
	f := &xcode.FakeRunner{}
	flags := config.Layer{Source: config.SourceFlag, Config: config.Config{Project: "Other.xcodeproj", Scheme: "Custom"}, BaseDir: root}

	p, err := Resolve(context.Background(), xcode.New(f), resolved(t, root, flags), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Container != (xcode.Container{Kind: xcode.Project, Path: filepath.Join(root, "Other.xcodeproj")}) || p.Scheme != "Custom" {
		t.Errorf("got %+v", p)
	}
	if n := len(f.Calls()); n != 0 {
		t.Errorf("configured scheme should not run xcodebuild -list; got %d calls", n)
	}
}

func TestResolveMissingConfiguredPath(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		layer config.Layer
		want  string
	}{
		{config.Layer{Source: config.SourceFlag, Config: config.Config{Workspace: "Nope.xcworkspace"}, BaseDir: root}, "(from --workspace) does not exist"},
		{config.Layer{Source: config.SourceEnv, Config: config.Config{Project: "Nope.xcodeproj"}, BaseDir: root}, "(from RUNWAY_PROJECT) does not exist"},
	} {
		_, err := ResolveContainer(resolved(t, root, tc.layer), nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("err = %v, want %q", err, tc.want)
		}
	}
}

func TestResolveNothingFound(t *testing.T) {
	_, err := ResolveContainer(resolved(t, t.TempDir()), nil)
	if err == nil || !strings.Contains(err.Error(), "no Xcode workspace or project found") {
		t.Errorf("err = %v", err)
	}
}

func TestResolvePickerCancelled(t *testing.T) {
	root := mkdirs(t, "A.xcodeproj", "B.xcodeproj")
	cancel := func(string, []string) (int, error) { return -1, ui.ErrCancelled }
	if _, err := ResolveContainer(resolved(t, root), cancel); !errors.Is(err, ui.ErrCancelled) {
		t.Errorf("err = %v, want ErrCancelled", err)
	}
}

func TestResolveNoSchemes(t *testing.T) {
	f := &xcode.FakeRunner{Default: xcode.FakeResult{Stdout: []byte(`{"project": {"name": "Empty", "schemes": []}}`)}}
	_, err := ResolveScheme(context.Background(), xcode.New(f), resolved(t, t.TempDir()), xcode.Container{Kind: xcode.Project, Path: "/x/Empty.xcodeproj"}, nil)
	if err == nil || !strings.Contains(err.Error(), "Empty.xcodeproj has no schemes") {
		t.Errorf("err = %v", err)
	}
}
