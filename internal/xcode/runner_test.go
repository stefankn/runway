package xcode

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestQuote(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"xcrun", "simctl", "list", "-j"}, "xcrun simctl list -j"},
		{[]string{"platform=iOS Simulator,id=ABC-123"}, "'platform=iOS Simulator,id=ABC-123'"},
		{[]string{"iPhone 17 Pro"}, "'iPhone 17 Pro'"},
		{[]string{"it's"}, `'it'\''s'`},
		{[]string{""}, "''"},
		{[]string{"$HOME"}, "'$HOME'"},
		{[]string{"*.app"}, "'*.app'"},
		{[]string{"/path/to/My App.app", "--flag=a,b"}, "'/path/to/My App.app' --flag=a,b"},
		{[]string{"subsystem == \"com.example\""}, `'subsystem == "com.example"'`},
	} {
		if got := Quote(tc.args...); got != tc.want {
			t.Errorf("Quote(%q) = %s, want %s", tc.args, got, tc.want)
		}
	}
}

func TestCmdString(t *testing.T) {
	c := Cmd{
		Name: "xcrun",
		Args: []string{"simctl", "launch", "My Sim"},
		Dir:  "/tmp/my project",
		Env:  []string{"SIMCTL_CHILD_FOO=a b"},
	}
	want := `cd '/tmp/my project' && SIMCTL_CHILD_FOO='a b' xcrun simctl launch 'My Sim'`
	if got := c.String(); got != want {
		t.Errorf("String() = %s, want %s", got, want)
	}
}

func TestVerboseEcho(t *testing.T) {
	ctx := context.Background()
	var log bytes.Buffer
	r := &ExecRunner{Verbose: true, Log: &log}
	if _, err := r.Output(ctx, Cmd{Name: "/bin/echo", Args: []string{"hello world"}}); err != nil {
		t.Fatal(err)
	}
	if want := "$ /bin/echo 'hello world'\n"; log.String() != want {
		t.Errorf("echo = %q, want %q", log.String(), want)
	}

	log.Reset()
	r.Verbose = false
	if _, err := r.Output(ctx, Cmd{Name: "/bin/echo"}); err != nil {
		t.Fatal(err)
	}
	if log.Len() != 0 {
		t.Errorf("echo with Verbose=false = %q, want nothing", log.String())
	}
}

func TestExecOutput(t *testing.T) {
	out, err := (&ExecRunner{}).Output(context.Background(), Cmd{Name: "/bin/echo", Args: []string{"a b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "a b c\n" {
		t.Errorf("output = %q", out)
	}
}

func TestExecOutputEnvAndDir(t *testing.T) {
	dir := t.TempDir()
	out, err := (&ExecRunner{}).Output(context.Background(), Cmd{
		Name: "/bin/sh", // test-only; runway itself never uses a shell
		Args: []string{"-c", `printf '%s|%s' "$RUNWAY_TEST" "$(pwd -P)"`},
		Dir:  dir,
		Env:  []string{"RUNWAY_TEST=x y"},
	})
	if err != nil {
		t.Fatal(err)
	}
	gotEnv, gotDir, _ := strings.Cut(string(out), "|")
	if gotEnv != "x y" {
		t.Errorf("env = %q", gotEnv)
	}
	if !strings.HasSuffix(gotDir, dir) { // macOS resolves /var to /private/var
		t.Errorf("dir = %q, want %q", gotDir, dir)
	}
}

func TestExecExitError(t *testing.T) {
	_, err := (&ExecRunner{}).Output(context.Background(), Cmd{
		Name: "/bin/sh",
		Args: []string{"-c", "echo boom >&2; exit 3"},
	})
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v (%T), want *ExitError", err, err)
	}
	if ee.ExitCode() != 3 {
		t.Errorf("code = %d, want 3", ee.ExitCode())
	}
	if string(ee.Stderr) != "boom\n" {
		t.Errorf("stderr = %q", ee.Stderr)
	}
	if want := "/bin/sh exited with status 3: boom"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}

	err = (&ExecRunner{}).Run(context.Background(), Cmd{Name: "/usr/bin/false"})
	if !errors.As(err, &ee) || ee.Code != 1 {
		t.Errorf("Run(false) err = %v, want *ExitError with code 1", err)
	}
}

func TestExecNotFound(t *testing.T) {
	err := (&ExecRunner{}).Run(context.Background(), Cmd{Name: "runway-no-such-tool"})
	var ee *ExitError
	if err == nil || errors.As(err, &ee) {
		t.Errorf("err = %v, want a non-ExitError", err)
	}
}

func TestExecRunStreams(t *testing.T) {
	var out bytes.Buffer
	err := (&ExecRunner{}).Run(context.Background(), Cmd{Name: "/bin/echo", Args: []string{"streamed"}, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "streamed\n" {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestExecCancelInterrupts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, err := (&ExecRunner{WaitDelay: time.Second}).Start(ctx, Cmd{Name: "/bin/sleep", Args: []string{"10"}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	cancel()
	err = p.Wait()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Wait() = %v, want context.Canceled", err)
	}
	// sleep dies on SIGINT, so this must be well under WaitDelay.
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("took %v to stop", d)
	}
}

func TestExecStartSignal(t *testing.T) {
	p, err := (&ExecRunner{}).Start(context.Background(), Cmd{Name: "/bin/sleep", Args: []string{"10"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	var ee *ExitError
	if err := p.Wait(); !errors.As(err, &ee) || ee.Code != -1 {
		t.Errorf("Wait() = %v, want *ExitError with code -1 (killed by signal)", err)
	}
}

func TestFakeRunnerRecordsAndResponds(t *testing.T) {
	ctx := context.Background()
	f := &FakeRunner{Default: FakeResult{Err: errors.New("unexpected command")}}
	f.Respond(FakeResult{Stdout: []byte(`{"devices":{}}`)}, "xcrun", "simctl", "list", "devices", "available", "-j")

	out, err := f.Output(ctx, Cmd{Name: "xcrun", Args: []string{"simctl", "list", "devices", "available", "-j"}})
	if err != nil || string(out) != `{"devices":{}}` {
		t.Errorf("Output = %q, %v", out, err)
	}
	if err := f.Run(ctx, Cmd{Name: "xcodebuild", Args: []string{"build"}}); err == nil {
		t.Error("unscripted command: want Default error")
	}

	want := [][]string{
		{"xcrun", "simctl", "list", "devices", "available", "-j"},
		{"xcodebuild", "build"},
	}
	var got [][]string
	for _, c := range f.Calls() {
		got = append(got, append([]string{c.Name}, c.Args...))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestFakeProcess(t *testing.T) {
	f := &FakeRunner{}
	f.Respond(FakeResult{Block: true, Stdout: []byte("hi\n")}, "log", "stream")

	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	p, err := f.Start(ctx, Cmd{Name: "log", Args: []string{"stream"}, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "hi\n" {
		t.Errorf("stdout = %q", out.String())
	}
	_ = p.Signal(os.Interrupt)
	if got := f.Processes()[0].Signals(); len(got) != 1 || got[0] != os.Interrupt {
		t.Errorf("signals = %v", got)
	}
	cancel()
	if err := p.Wait(); !errors.Is(err, context.Canceled) {
		t.Errorf("Wait() = %v, want context.Canceled", err)
	}

	// Non-blocking start exits immediately with the scripted error.
	f.Default = FakeResult{Err: errors.New("exit 1")}
	p, _ = f.Start(context.Background(), Cmd{Name: "other"})
	if err := p.Wait(); err == nil || err.Error() != "exit 1" {
		t.Errorf("Wait() = %v", err)
	}
}

func TestLookPath(t *testing.T) {
	if p, err := (&ExecRunner{}).LookPath("sh"); err != nil || p == "" {
		t.Errorf("ExecRunner.LookPath(sh) = %q, %v", p, err)
	}
	f := &FakeRunner{Paths: map[string]string{"xcbeautify": "/opt/homebrew/bin/xcbeautify"}}
	if p, err := f.LookPath("xcbeautify"); err != nil || p != "/opt/homebrew/bin/xcbeautify" {
		t.Errorf("FakeRunner.LookPath(xcbeautify) = %q, %v", p, err)
	}
	if _, err := f.LookPath("missing"); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("FakeRunner.LookPath(missing) err = %v", err)
	}
}

func TestFakeStartEchoesStdin(t *testing.T) {
	f := &FakeRunner{Default: FakeResult{EchoStdin: true}}
	var out bytes.Buffer
	p, err := f.Start(context.Background(), Cmd{Name: "cat", Stdin: strings.NewReader("a\nb\n"), Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "a\nb\n" {
		t.Errorf("stdout = %q", out.String())
	}
}
