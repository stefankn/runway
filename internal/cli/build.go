package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/destination"
	"github.com/stefankn/runway/internal/project"
	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

func newBuildCommand(opts *options) *cobra.Command {
	var (
		pf         projectFlags
		df         destFlags
		raw, quiet bool
	)
	cmd := &cobra.Command{
		Use:   "build [flags] [-- xcodebuild arguments]",
		Short: "Build the app for a simulator or device",
		Long: `Build the app with xcodebuild, streaming its output. If xcbeautify is installed,
the output is piped through it (use --raw to see xcodebuild's own output).

Everything after -- is passed to xcodebuild unchanged.`,
		Example: `  runway build --sim
  runway build --sim "iPhone 18 Pro"
  runway build --device --team ABCDE12345
  runway build -- -showBuildTimingSummary`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := opts.resolveTarget(cmd, args, &pf, &df)
			if err != nil {
				return err
			}
			bo := t.buildOptions(quiet)
			bo.ExtraArgs = argsAfterDash(cmd, args)
			app, err := opts.buildApp(cmd, t, bo, raw)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  App:       %s\n  Bundle ID: %s\n", displayPath(t.cwd, app.Path), app.BundleID)
			return nil
		},
	}
	pf.register(cmd)
	df.register(cmd)
	cmd.Flags().BoolVar(&raw, "raw", false, "show xcodebuild's output without xcbeautify")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "only show errors and the result")
	return cmd
}

// target is what to build and where: shared by build and run.
type target struct {
	client  *xcode.Client
	project project.Project
	dest    destination.Destination
	cwd     string
}

// resolveTarget resolves the project (flags, env, .runway.toml, detection)
// and the destination (--sim/--device/--destination, config, picker).
func (o *options) resolveTarget(cmd *cobra.Command, args []string, pf *projectFlags, df *destFlags) (target, error) {
	ctx := cmd.Context()
	cfg, err := o.resolveConfig(pf)
	if err != nil {
		return target{}, err
	}
	req, err := df.request(cmd, args, &cfg)
	if err != nil {
		return target{}, err
	}
	cwd, err := o.cwd()
	if err != nil {
		return target{}, err
	}
	client, pick := o.client(cmd), o.picker(cmd)
	p, err := project.Resolve(ctx, client, &cfg, pick)
	if err != nil {
		return target{}, err
	}
	dest, err := destination.Resolve(ctx, client, req, pick)
	if err != nil {
		return target{}, err
	}
	return target{client: client, project: p, dest: dest, cwd: cwd}, nil
}

func (t target) buildOptions(quiet bool) xcode.BuildOptions {
	return xcode.BuildOptions{
		Container:     t.project.Container,
		Scheme:        t.project.Scheme,
		Configuration: t.project.Configuration,
		Destination:   t.dest.Spec,
		DerivedData:   t.project.DerivedData,
		Team:          t.project.Team,
		Quiet:         quiet,
	}
}

// buildApp builds with live output (through xcbeautify when it's installed
// and raw is false), prints hints after a failure, and returns the built app.
func (o *options) buildApp(cmd *cobra.Command, t target, bo xcode.BuildOptions, raw bool) (xcode.AppProduct, error) {
	ctx := cmd.Context()
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	errSt, outSt := ui.NewStyler(stderr), ui.NewStyler(stdout)

	beautify := false
	if !raw {
		_, lookErr := o.runner(stderr).LookPath("xcbeautify")
		beautify = lookErr == nil
	}
	if !bo.Quiet {
		fmt.Fprintln(stderr, errSt.Bold(fmt.Sprintf("Building %s (%s) for %s", bo.Scheme, bo.Configuration, t.dest)))
	}
	if beautify && o.verbose {
		fmt.Fprintln(stderr, errSt.Dim("output piped through xcbeautify (use --raw to disable)"))
	}

	hints := newHintScanner()
	err := t.client.Build(ctx, bo, xcode.BuildOutput{
		Stdout: stdout, Stderr: stderr, Beautify: beautify, Color: outSt.Enabled, Tap: hints,
	})
	if err != nil {
		if ctx.Err() == nil {
			for _, h := range hints.Hints() {
				fmt.Fprintln(stderr, errSt.Yellow("hint: ")+h)
			}
		}
		return xcode.AppProduct{}, err
	}
	// xcbeautify already ends with "Build Succeeded", except in --quiet mode.
	if !beautify || bo.Quiet {
		fmt.Fprintln(stdout, outSt.Green("Build succeeded"))
	}
	return t.client.AppProduct(ctx, bo)
}
