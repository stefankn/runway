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
			ctx := cmd.Context()
			cfg, err := opts.resolveConfig(&pf)
			if err != nil {
				return err
			}
			req, err := df.request(cmd, args, &cfg)
			if err != nil {
				return err
			}
			client, pick := opts.client(cmd), opts.picker(cmd)
			p, err := project.Resolve(ctx, client, &cfg, pick)
			if err != nil {
				return err
			}
			dest, err := destination.Resolve(ctx, client, req, pick)
			if err != nil {
				return err
			}

			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			errSt, outSt := ui.NewStyler(stderr), ui.NewStyler(stdout)
			bo := xcode.BuildOptions{
				Container:     p.Container,
				Scheme:        p.Scheme,
				Configuration: p.Configuration,
				Destination:   dest.Spec,
				DerivedData:   p.DerivedData,
				Team:          p.Team,
				Quiet:         quiet,
				ExtraArgs:     argsAfterDash(cmd, args),
			}
			beautify := false
			if !raw {
				_, lookErr := opts.runner(stderr).LookPath("xcbeautify")
				beautify = lookErr == nil
			}
			if !quiet {
				fmt.Fprintln(stderr, errSt.Bold(fmt.Sprintf("Building %s (%s) for %s", p.Scheme, p.Configuration, dest)))
			}
			if beautify && opts.verbose {
				fmt.Fprintln(stderr, errSt.Dim("output piped through xcbeautify (use --raw to disable)"))
			}

			hints := newHintScanner()
			err = client.Build(ctx, bo, xcode.BuildOutput{
				Stdout: stdout, Stderr: stderr, Beautify: beautify, Color: outSt.Enabled, Tap: hints,
			})
			if err != nil {
				if ctx.Err() == nil {
					for _, h := range hints.Hints() {
						fmt.Fprintln(stderr, errSt.Yellow("hint: ")+h)
					}
				}
				return err
			}

			app, err := client.AppProduct(ctx, bo)
			if err != nil {
				return err
			}
			cwd, err := opts.cwd()
			if err != nil {
				return err
			}
			// xcbeautify already ends with "Build Succeeded", except in --quiet mode.
			if !beautify || quiet {
				fmt.Fprintln(stdout, outSt.Green("Build succeeded"))
			}
			fmt.Fprintf(stdout, "  App:       %s\n  Bundle ID: %s\n", displayPath(cwd, app.Path), app.BundleID)
			return nil
		},
	}
	pf.register(cmd)
	df.register(cmd)
	cmd.Flags().BoolVar(&raw, "raw", false, "show xcodebuild's output without xcbeautify")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "only show errors and the result")
	return cmd
}
