package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

// How long runway waits for the app to be terminated after Ctrl-C. Network
// devices are slower to answer than simulators.
const (
	simStopTimeout    = 5 * time.Second
	deviceStopTimeout = 10 * time.Second
)

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
		Long: `Build the app, install it on a simulator or device and launch it, like
Xcode's Run button. The app's output (print, stdout and stderr) streams to this terminal
until the app exits or you press Ctrl-C, which also stops the app.

Running again replaces the running copy. Everything after -- is passed to the
app as launch arguments.`,
		Example: `  runway run --sim
  runway run --sim "iPhone 18 Pro" --env API_URL=http://localhost:8080
  runway run --device "iPhone 13"
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
			if t.dest.Simulator == nil && t.dest.Device == nil {
				return errors.New("run needs --sim or --device (--destination only works with build)")
			}

			ctx := cmd.Context()
			stderr := cmd.ErrOrStderr()
			st := ui.NewStyler(stderr)
			s := session{
				cmd: cmd, t: t, appArgs: argsAfterDash(cmd, args), env: env, headless: headless,
				stderr: stderr, st: st,
				status: func(format string, a ...any) {
					if !quiet {
						fmt.Fprintln(stderr, st.Bold(fmt.Sprintf(format, a...)))
					}
				},
			}

			bo := t.buildOptions(quiet)
			if noBuild {
				if s.app, err = t.client.AppProduct(ctx, bo); err != nil {
					return err
				}
				if _, statErr := os.Stat(s.app.Path); statErr != nil {
					return fmt.Errorf("no build found at %s; run without --no-build", displayPath(t.cwd, s.app.Path))
				}
			} else if s.app, err = opts.buildApp(cmd, t, bo, raw); err != nil {
				return err
			}

			if t.dest.Simulator != nil {
				return s.runOnSimulator(*t.dest.Simulator)
			}
			return s.runOnDevice(*t.dest.Device)
		},
	}
	pf.register(cmd)
	df.register(cmd)
	f := cmd.Flags()
	f.BoolVar(&noBuild, "no-build", false, "launch the last build without building first")
	f.BoolVar(&headless, "headless", false, "don't bring the simulator window (DeviceHub) to the front (simulators only)")
	f.StringArrayVar(&env, "env", nil, "set an environment variable for the app (KEY=VALUE, repeatable)")
	f.BoolVar(&raw, "raw", false, "show xcodebuild's output without xcbeautify")
	f.BoolVar(&quiet, "quiet", false, "only show build errors and the app's output")
	return cmd
}

// session is one `runway run`: the built app and how to launch it.
type session struct {
	cmd      *cobra.Command
	t        target
	app      xcode.AppProduct
	appArgs  []string
	env      []string
	headless bool
	stderr   io.Writer
	st       ui.Styler
	status   func(format string, a ...any)
}

func (s *session) runOnSimulator(sim xcode.Simulator) error {
	ctx, client := s.cmd.Context(), s.t.client
	if !sim.Booted() {
		s.status("Booting %s…", sim.Name)
	}
	if err := client.SimBoot(ctx, sim.UDID); err != nil {
		return err
	}
	if !s.headless {
		if err := client.OpenDeviceHub(ctx); err != nil {
			fmt.Fprintln(s.stderr, s.st.Yellow("warning: ")+err.Error())
		}
	}
	s.status("Installing %s…", filepath.Base(s.app.Path))
	if err := client.SimInstall(ctx, sim.UDID, s.app.Path); err != nil {
		return err
	}

	s.launching(sim.Name)
	// The app's stdin is not connected: a console that put the terminal in
	// raw mode would swallow Ctrl-C.
	p, err := client.SimLaunch(ctx, sim.UDID, s.app.BundleID, s.appArgs, s.env,
		xcode.ConsoleIO{Stdout: s.cmd.OutOrStdout(), Stderr: s.stderr})
	if err != nil {
		return err
	}
	return finish(ctx, p.Wait(), simStopTimeout, func(stopCtx context.Context) {
		// simctl has passed SIGINT on to the app (which quits); terminating
		// too is a safety net, like Xcode's Stop button.
		_ = client.SimTerminate(stopCtx, sim.UDID, s.app.BundleID)
	})
}

func (s *session) runOnDevice(dev xcode.Device) error {
	ctx, client := s.cmd.Context(), s.t.client
	s.status("Installing %s on %s (%s)…", filepath.Base(s.app.Path), dev.Name, dev.Connection)
	if err := client.DeviceInstall(ctx, dev.UDID, s.app.Path); err != nil {
		s.printHints(err)
		return err
	}

	s.launching(dev.Name)
	// devicectl's error messages (locked device, untrusted developer, …) go to
	// stderr; watch them for hints.
	hints := newHintScanner()
	p, err := client.DeviceLaunch(ctx, dev.UDID, s.app.BundleID, s.appArgs, s.env,
		xcode.ConsoleIO{Stdout: s.cmd.OutOrStdout(), Stderr: io.MultiWriter(s.stderr, hints)})
	if err != nil {
		return err
	}
	waitErr := p.Wait()
	if waitErr != nil && ctx.Err() == nil {
		for _, h := range hints.Hints() {
			fmt.Fprintln(s.stderr, s.st.Yellow("hint: ")+h)
		}
	}
	return finish(ctx, waitErr, deviceStopTimeout, func(stopCtx context.Context) {
		// devicectl forwards SIGINT to the app; as a safety net, find the
		// app's process and terminate it. Errors mean it has already quit.
		if pid, found, err := client.DeviceAppPID(stopCtx, dev.UDID, s.app); err == nil && found {
			_ = client.DeviceTerminate(stopCtx, dev.UDID, pid)
		}
	})
}

func (s *session) launching(where string) {
	fmt.Fprintln(s.stderr, s.st.Dim(fmt.Sprintf("Launching %s on %s (Ctrl-C to stop)", s.app.BundleID, where)))
}

// printHints prints hints matching a failed command's error and stderr.
func (s *session) printHints(err error) {
	h := newHintScanner()
	_, _ = io.WriteString(h, err.Error()+"\n")
	if ee, ok := errors.AsType[*xcode.ExitError](err); ok {
		_, _ = h.Write(ee.Stderr)
	}
	for _, text := range h.Hints() {
		fmt.Fprintln(s.stderr, s.st.Yellow("hint: ")+text)
	}
}

// finish turns the end of a launched app into runway's result. After Ctrl-C
// (ctx cancelled) it calls stop with a fresh, time-limited context and returns
// the cancellation; otherwise the app's exit status becomes runway's.
func finish(ctx context.Context, waitErr error, timeout time.Duration, stop func(context.Context)) error {
	if ctx.Err() != nil {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		stop(stopCtx)
		return ctx.Err()
	}
	if ee, ok := errors.AsType[*xcode.ExitError](waitErr); ok {
		return &appExitError{code: ee.Code}
	}
	return waitErr
}
