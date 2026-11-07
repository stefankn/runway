package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

// stopTimeout bounds how long runway waits for the app to be terminated
// after Ctrl-C.
const stopTimeout = 5 * time.Second

var envVarRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// appExitError reports that the launched app exited unsuccessfully. runway
// exits with the same status.
type appExitError struct{ code int }

func (e *appExitError) Error() string {
	if e.code < 0 {
		return "app was terminated by a signal"
	}
	return fmt.Sprintf("app exited with status %d", e.code)
}

func (e *appExitError) ExitCode() int { return e.code }

func newRunCommand(opts *options) *cobra.Command {
	var (
		pf                            projectFlags
		df                            destFlags
		noBuild, headless, raw, quiet bool
		env                           []string
	)
	cmd := &cobra.Command{
		Use:   "run [flags] [-- app arguments]",
		Short: "Build, install and launch the app, then stream its console",
		Long: `Build the app, install it on a simulator and launch it, like Xcode's Run
button. The app's output (print, stdout and stderr) streams to this terminal
until the app exits or you press Ctrl-C, which also stops the app.

Running again replaces the running copy. Everything after -- is passed to the
app as launch arguments.`,
		Example: `  runway run --sim
  runway run --sim "iPhone 18 Pro" --env API_URL=http://localhost:8080
  runway run --no-build -- -FeatureFlag YES`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, kv := range env {
				if !envVarRE.MatchString(kv) {
					return fmt.Errorf("invalid --env %q: use KEY=VALUE", kv)
				}
			}
			t, err := opts.resolveTarget(cmd, args, &pf, &df)
			if err != nil {
				return err
			}
			switch {
			case t.dest.Device != nil:
				return errors.New("running on devices arrives in the next milestone; use `runway build --device` for now")
			case t.dest.Simulator == nil:
				return errors.New("run needs --sim or --device (--destination only works with build)")
			}
			sim := *t.dest.Simulator

			ctx := cmd.Context()
			stderr := cmd.ErrOrStderr()
			st := ui.NewStyler(stderr)
			status := func(format string, a ...any) {
				if !quiet {
					fmt.Fprintln(stderr, st.Bold(fmt.Sprintf(format, a...)))
				}
			}

			bo := t.buildOptions(quiet)
			var app xcode.AppProduct
			if noBuild {
				if app, err = t.client.AppProduct(ctx, bo); err != nil {
					return err
				}
				if _, statErr := os.Stat(app.Path); statErr != nil {
					return fmt.Errorf("no build found at %s; run without --no-build", displayPath(t.cwd, app.Path))
				}
			} else if app, err = opts.buildApp(cmd, t, bo, raw); err != nil {
				return err
			}

			if !sim.Booted() {
				status("Booting %s…", sim.Name)
			}
			if err := t.client.SimBoot(ctx, sim.UDID); err != nil {
				return err
			}
			if !headless {
				if err := t.client.OpenDeviceHub(ctx); err != nil {
					fmt.Fprintln(stderr, st.Yellow("warning: ")+err.Error())
				}
			}
			status("Installing %s…", filepath.Base(app.Path))
			if err := t.client.SimInstall(ctx, sim.UDID, app.Path); err != nil {
				return err
			}

			fmt.Fprintln(stderr, st.Dim(fmt.Sprintf("Launching %s on %s (Ctrl-C to stop)", app.BundleID, sim.Name)))
			// The app's stdin is not connected: a console that put the terminal
			// in raw mode would swallow Ctrl-C.
			p, err := t.client.SimLaunch(ctx, sim.UDID, app.BundleID, argsAfterDash(cmd, args), env,
				xcode.ConsoleIO{Stdout: cmd.OutOrStdout(), Stderr: stderr})
			if err != nil {
				return err
			}
			waitErr := p.Wait()

			if ctx.Err() != nil {
				// Ctrl-C: simctl has passed SIGINT on to the app. Terminate it
				// explicitly too, like Xcode's Stop button; an error just means
				// it's already gone.
				stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
				defer cancel()
				_ = t.client.SimTerminate(stopCtx, sim.UDID, app.BundleID)
				return ctx.Err()
			}
			if ee, ok := errors.AsType[*xcode.ExitError](waitErr); ok {
				return &appExitError{code: ee.Code}
			}
			return waitErr
		},
	}
	pf.register(cmd)
	df.register(cmd)
	f := cmd.Flags()
	f.BoolVar(&noBuild, "no-build", false, "launch the last build without building first")
	f.BoolVar(&headless, "headless", false, "don't bring the simulator window (DeviceHub) to the front")
	f.StringArrayVar(&env, "env", nil, "set an environment variable for the app (KEY=VALUE, repeatable)")
	f.BoolVar(&raw, "raw", false, "show xcodebuild's output without xcbeautify")
	f.BoolVar(&quiet, "quiet", false, "only show build errors and the app's output")
	return cmd
}
