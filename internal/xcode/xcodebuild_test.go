package xcode

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseList(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		want    ProjectInfo
	}{
		{"xcodebuild/list-project.json", ProjectInfo{
			Name: "ActorTest", Schemes: []string{"ActorTest"}, Targets: []string{"ActorTest"},
			Configurations: []string{"Debug", "Release"},
		}},
		{"xcodebuild/list-project-multi.json", ProjectInfo{
			Name:           "Sensors",
			Schemes:        []string{"MenuBarPlugin", "Sensors", "SensorsWidgetExtension"},
			Targets:        []string{"Sensors", "MenuBarPlugin", "SensorsWidgetExtension"},
			Configurations: []string{"Debug", "Release"},
		}},
		{"xcodebuild/list-workspace.json", ProjectInfo{
			Name:    "Scratch",
			Schemes: []string{"ActorTest", "MenuBarPlugin", "Sensors", "SensorsWidgetExtension"},
		}},
	} {
		got, err := parseList(readFixture(t, tc.fixture))
		if err != nil {
			t.Fatalf("%s: %v", tc.fixture, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.fixture, got, tc.want)
		}
	}
}

func TestParseListErrors(t *testing.T) {
	for _, doc := range []string{"not json", "{}"} {
		if _, err := parseList([]byte(doc)); err == nil {
			t.Errorf("parseList(%q): want error", doc)
		}
	}
}

func TestListArgs(t *testing.T) {
	for _, tc := range []struct {
		c    Container
		want string
	}{
		{Container{Workspace, "/p/My App.xcworkspace"}, "xcodebuild -list -json -workspace '/p/My App.xcworkspace'"},
		{Container{Project, "/p/MyApp.xcodeproj"}, "xcodebuild -list -json -project /p/MyApp.xcodeproj"},
	} {
		f := &FakeRunner{Default: FakeResult{Stdout: readFixture(t, "xcodebuild/list-project.json")}}
		if _, err := New(f).List(context.Background(), tc.c); err != nil {
			t.Fatal(err)
		}
		if got := f.Calls()[0].String(); got != tc.want {
			t.Errorf("command = %s, want %s", got, tc.want)
		}
	}
}

func TestListError(t *testing.T) {
	f := &FakeRunner{Default: FakeResult{Err: &ExitError{Cmd: Cmd{Name: "xcodebuild"}, Code: 66, Stderr: []byte("xcodebuild: error: 'X.xcodeproj' does not exist.")}}}
	_, err := New(f).List(context.Background(), Container{Project, "X.xcodeproj"})
	var ee *ExitError
	if !errors.As(err, &ee) || !strings.HasPrefix(err.Error(), "reading schemes from X.xcodeproj: xcodebuild exited with status 66") {
		t.Errorf("err = %v", err)
	}
}
