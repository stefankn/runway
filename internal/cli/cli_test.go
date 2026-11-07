package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stefankn/runway/internal/xcode"
)

var testInfo = BuildInfo{Version: "v1.2.3", Commit: "abc1234", Date: "2026-11-07"}

const wantVersion = "runway v1.2.3 (commit abc1234, built 2026-11-07)\n"

func TestVersionCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := execute(testInfo, []string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	if out.String() != wantVersion {
		t.Errorf("output = %q, want %q", out.String(), wantVersion)
	}
}

func TestVersionFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := execute(testInfo, []string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	if out.String() != wantVersion {
		t.Errorf("output = %q, want %q", out.String(), wantVersion)
	}
}

func TestVersionRejectsArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := execute(testInfo, []string{"version", "extra"}, &out, &errOut); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := execute(testInfo, []string{"bogus"}, &out, &errOut)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.HasPrefix(errOut.String(), "runway: unknown command \"bogus\"") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestVerboseFlagReachesRunner(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"version"}, false},
		{[]string{"-v", "version"}, true},
		{[]string{"version", "--verbose"}, true},
	} {
		opts := &options{}
		root := newRootCommand(testInfo, opts)
		root.SetArgs(tc.args)
		root.SetOut(&bytes.Buffer{})
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		r, ok := opts.runner(&bytes.Buffer{}).(*xcode.ExecRunner)
		if !ok {
			t.Fatalf("runner is %T, want *xcode.ExecRunner", opts.runner(nil))
		}
		if r.Verbose != tc.want {
			t.Errorf("%v: Verbose = %v, want %v", tc.args, r.Verbose, tc.want)
		}
	}
}
