package xcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// BuildOptions describe one xcodebuild build.
type BuildOptions struct {
	Container     Container
	Scheme        string
	Configuration string
	Destination   string // -destination value
	DerivedData   string
	Team          string // DEVELOPMENT_TEAM; optional
	Quiet         bool
	ExtraArgs     []string // passed to xcodebuild unchanged, after runway's own
}

// settingsArgs are the arguments shared by build and -showBuildSettings.
func (o BuildOptions) settingsArgs() []string {
	args := append([]string{}, o.Container.Args()...)
	return append(args,
		"-scheme", o.Scheme,
		"-configuration", o.Configuration,
		"-destination", o.Destination,
		"-derivedDataPath", o.DerivedData,
	)
}

// BuildArgs returns the xcodebuild arguments for a build.
func BuildArgs(o BuildOptions) []string {
	args := append([]string{"build"}, o.settingsArgs()...)
	args = append(args, "-allowProvisioningUpdates")
	if o.Quiet {
		args = append(args, "-quiet")
	}
	if o.Team != "" {
		args = append(args, "DEVELOPMENT_TEAM="+o.Team)
	}
	return append(args, o.ExtraArgs...)
}

// BuildOutput says where build output goes.
type BuildOutput struct {
	Stdout, Stderr io.Writer
	Beautify       bool      // pipe xcodebuild's output through xcbeautify
	Color          bool      // let xcbeautify use colours
	Tap            io.Writer // optional: receives a copy of the raw xcodebuild output
}

// ErrBuildFailed wraps xcodebuild failures from Build.
var ErrBuildFailed = errors.New("build failed")

// Build runs xcodebuild build, streaming its output as it arrives. When
// out.Beautify is set, stdout and stderr are piped through xcbeautify. The
// returned error keeps xcodebuild's *ExitError, so its exit status propagates.
func (c *Client) Build(ctx context.Context, o BuildOptions, out BuildOutput) error {
	cmd := Cmd{Name: "xcodebuild", Args: BuildArgs(o), Stdout: out.Stdout, Stderr: out.Stderr}
	tee := func(w io.Writer) io.Writer {
		if out.Tap == nil || w == nil {
			return w
		}
		return io.MultiWriter(w, out.Tap)
	}

	if !out.Beautify {
		cmd.Stdout, cmd.Stderr = tee(out.Stdout), tee(out.Stderr)
		return buildErr(c.r.Run(ctx, cmd))
	}

	pr, pw := io.Pipe()
	fmtArgs := []string{"--disable-logging"}
	if o.Quiet {
		fmtArgs = append(fmtArgs, "--quieter")
	}
	if !out.Color {
		fmtArgs = append(fmtArgs, "--disable-colored-output")
	}
	formatter, err := c.r.Start(ctx, Cmd{Name: "xcbeautify", Args: fmtArgs, Stdin: pr, Stdout: out.Stdout, Stderr: out.Stderr})
	if err != nil {
		return fmt.Errorf("starting xcbeautify: %w", err)
	}
	cmd.Stdout, cmd.Stderr = tee(pw), tee(pw)
	buildErrResult := c.r.Run(ctx, cmd)
	_ = pw.Close()
	fmtErr := formatter.Wait()
	_ = pr.Close()
	if buildErrResult != nil {
		return buildErr(buildErrResult)
	}
	if fmtErr != nil {
		return fmt.Errorf("xcbeautify: %w", fmtErr)
	}
	return nil
}

func buildErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrBuildFailed, err)
}

// AppProduct is the app a build produced.
type AppProduct struct {
	Path     string // …/Build/Products/Debug-iphonesimulator/MyApp.app
	BundleID string
	Target   string
	Platform string // PLATFORM_NAME, e.g. "iphonesimulator", "iphoneos"
}

// AppProduct asks xcodebuild where the scheme's app is, using the same
// settings as the build. KEY=VALUE build settings among o.ExtraArgs are
// included because they can change output paths.
func (c *Client) AppProduct(ctx context.Context, o BuildOptions) (AppProduct, error) {
	args := append([]string{"-showBuildSettings", "-json"}, o.settingsArgs()...)
	for _, a := range o.ExtraArgs {
		if k, _, ok := strings.Cut(a, "="); ok && k != "" && !strings.HasPrefix(k, "-") {
			args = append(args, a)
		}
	}
	data, err := c.r.Output(ctx, Cmd{Name: "xcodebuild", Args: args})
	if err != nil {
		return AppProduct{}, fmt.Errorf("reading build settings: %w", err)
	}
	return parseBuildSettings(data, o.Scheme)
}

// Output of `xcodebuild -showBuildSettings -json`.
type rawBuildSettings []struct {
	Target        string            `json:"target"`
	BuildSettings map[string]string `json:"buildSettings"`
}

func parseBuildSettings(data []byte, scheme string) (AppProduct, error) {
	var raw rawBuildSettings
	if err := json.Unmarshal(data, &raw); err != nil {
		return AppProduct{}, fmt.Errorf("parsing build settings JSON: %w", err)
	}
	var apps []AppProduct
	for _, e := range raw {
		s := e.BuildSettings
		if s["WRAPPER_EXTENSION"] != "app" {
			continue
		}
		apps = append(apps, AppProduct{
			Path:     filepath.Join(s["TARGET_BUILD_DIR"], s["WRAPPER_NAME"]),
			BundleID: s["PRODUCT_BUNDLE_IDENTIFIER"],
			Target:   e.Target,
			Platform: s["PLATFORM_NAME"],
		})
	}
	if len(apps) == 0 {
		return AppProduct{}, fmt.Errorf("scheme %s doesn't build an app (no target with WRAPPER_EXTENSION = app)", scheme)
	}
	for _, a := range apps {
		if a.Target == scheme {
			return a, nil
		}
	}
	return apps[0], nil
}
