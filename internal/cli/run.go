package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
		pf                                  projectFlags
		df                                  destFlags
		noBuild, headless, raw, quiet, logs bool
		env                                 []string
		timeout                             time.Duration
	)
	cmd := &cobra.Command{
		Use:   "run [flags] [-- app arguments]",
		Short: "Build, install and launch the app, then stream its console",
		Long: `Build the app, install it on a simulator or device and launch it, like
Xcode's Run button. The app's output (print, stdout and stderr) streams to this terminal
until the app exits or you press Ctrl-C, which also stops the app.

With --logs, the app's unified log (Logger, os_log) is shown too, each line
prefixed with "log │". Running again replaces the running copy. Everything
after -- is passed to the app as launch arguments.

With --timeout, runway stops the app after it has run that long and exits
with status 0, as if you had pressed Ctrl-C. The time starts when the app
launches, so the build doesn't count. This suits scripts and AI agents that
want the app's output without waiting for Ctrl-C.`,
		Example: `  runway run --sim
  runway run --sim "iPhone 18 Pro" --env API_URL=http://localhost:8080
  runway run --device "iPhone 13"
  runway run --sim --logs
  runway run --no-build -- -FeatureFlag YES
  runway run --sim --headless --timeout 30s`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout < 0 {
				return fmt.Errorf("invalid --timeout %s: must not be negative", timeout)
			}
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
				cmd: cmd, t: t, appArgs: argsAfterDash(cmd, args), env: env, headless: headless, logs: logs, timeout: timeout,
				stdout: cmd.OutOrStdout(), stderr: stderr, st: st,
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
	f.BoolVar(&logs, "logs", false, "also show the app's unified log (Logger, os_log)")
	f.DurationVar(&timeout, "timeout", 0, "stop the app after it has run this long, e.g. 30s (0: no limit)")
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
	logs     bool
	timeout  time.Duration // 0: run until the app exits or Ctrl-C
	stdout   io.Writer
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

	if s.logs {
		stop := s.startSimLogs(sim)
		defer stop()
	}

	s.launching(sim.Name)
	ctx, cancel := s.launchContext()
	defer cancel()
	// The app's stdin is not connected: a console that put the terminal in
	// raw mode would swallow Ctrl-C.
	p, err := client.SimLaunch(ctx, sim.UDID, s.app.BundleID, s.appArgs, s.env,
		xcode.ConsoleIO{Stdout: s.stdout, Stderr: s.stderr})
	if err != nil {
		return err
	}
	return s.finish(ctx, p.Wait(), simStopTimeout, func(stopCtx context.Context) {
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

	env := s.env
	if s.logs {
		// Devices can't stream their log to the Mac. This environment
		// variable makes the app mirror its Logger / os_log messages to
		// stderr, which devicectl's console shows (see docs/device-logs.md).
		env = append(slices.Clone(env), "OS_ACTIVITY_DT_MODE=1")
		fmt.Fprintln(s.stderr, s.st.Dim("--logs on a device: Logger messages are mirrored to the app's console (OS_ACTIVITY_DT_MODE=1)"))
	}

	s.launching(dev.Name)
	ctx, cancel := s.launchContext()
	defer cancel()
	// devicectl's error messages (locked device, untrusted developer, …) go to
	// stderr; watch them for hints.
	hints := newHintScanner()
	p, err := client.DeviceLaunch(ctx, dev.UDID, s.app.BundleID, s.appArgs, env,
		xcode.ConsoleIO{Stdout: s.stdout, Stderr: io.MultiWriter(s.stderr, hints)})
	if err != nil {
		return err
	}
	waitErr := p.Wait()
	if waitErr != nil && ctx.Err() == nil {
		for _, h := range hints.Hints() {
			fmt.Fprintln(s.stderr, s.st.Yellow("hint: ")+h)
		}
	}
	return s.finish(ctx, waitErr, deviceStopTimeout, func(stopCtx context.Context) {
		// devicectl forwards SIGINT to the app; as a safety net, find the
		// app's process and terminate it. Errors mean it has already quit.
		if pid, found, err := client.DeviceAppPID(stopCtx, dev.UDID, s.app); err == nil && found {
			_ = client.DeviceTerminate(stopCtx, dev.UDID, pid)
		}
	})
}

// logDrain is how long run --logs keeps streaming after the app has exited
// by itself, for its last messages to arrive.
var logDrain = 500 * time.Millisecond

// startSimLogs streams the app's unified log into the run output, each line
// prefixed with "log │". It returns once the stream is running, so messages
// from the app's launch are seen. A failure is a warning: the app still runs.
// The returned function stops the stream.
func (s *session) startSimLogs(sim xcode.Simulator) (stop func()) {
	ctx := s.cmd.Context()
	sub := s.t.logSubsystem
	if sub == "" {
		sub = s.app.BundleID
	}
	// The console and the log stream write from different goroutines.
	out := &syncWriter{w: s.stdout}
	s.stdout = out
	printer := &logPrinter{w: out, st: ui.NewStyler(s.cmd.OutOrStdout()), prefix: ui.NewStyler(s.cmd.OutOrStdout()).Dim("log │ ")}
	pw := printer.writer()
	ls, err := s.t.client.SimLogStream(ctx, sim.UDID, xcode.LogFilter{Subsystem: sub},
		xcode.LogOutput{Stdout: pw, Stderr: s.stderr})
	if err != nil {
		fmt.Fprintln(s.stderr, s.st.Yellow("warning: ")+err.Error())
		return func() {}
	}
	ls.WaitStarted(ctx)
	return func() {
		// A stream that ended before the app did failed (e.g. a bad
		// predicate); one that runway stops ends with an error by design.
		var endedEarly bool
		select {
		case <-ls.Done():
			endedEarly = true
		default:
		}
		if !endedEarly && ctx.Err() == nil {
			select {
			case <-ls.Done():
			case <-time.After(logDrain):
			}
		}
		stopLogStream(ctx, ls)
		pw.Flush()
		if err := ls.Wait(); endedEarly && err != nil {
			fmt.Fprintln(s.stderr, s.st.Yellow("warning: ")+err.Error())
		}
	}
}

func (s *session) launching(where string) {
	stop := "Ctrl-C to stop"
	if s.timeout > 0 {
		stop = fmt.Sprintf("stops after %s, or Ctrl-C", s.timeout)
	}
	fmt.Fprintln(s.stderr, s.st.Dim(fmt.Sprintf("Launching %s on %s (%s)", s.app.BundleID, where, stop)))
}

// errRunTimeout is the cancellation cause when --timeout ends the run.
var errRunTimeout = errors.New("run timeout")

// launchContext returns the context the launched app runs under. With
// --timeout it ends after that long, which stops the app as Ctrl-C does.
func (s *session) launchContext() (context.Context, context.CancelFunc) {
	ctx := s.cmd.Context()
	if s.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, s.timeout, errRunTimeout)
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

// finish turns the end of a launched app into runway's result. ctx is the
// launch context. When it has ended (Ctrl-C or --timeout), finish calls stop
// with a fresh, time-limited context; after Ctrl-C it returns the
// cancellation, after --timeout nil. Otherwise the app's exit status becomes
// runway's.
func (s *session) finish(ctx context.Context, waitErr error, timeout time.Duration, stop func(context.Context)) error {
	if ctx.Err() != nil {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		stop(stopCtx)
		if errors.Is(context.Cause(ctx), errRunTimeout) {
			fmt.Fprintln(s.stderr, s.st.Dim(fmt.Sprintf("Stopped the app after %s (--timeout)", s.timeout)))
			return nil
		}
		return ctx.Err()
	}
	if ee, ok := errors.AsType[*xcode.ExitError](waitErr); ok {
		return &appExitError{code: ee.Code}
	}
	return waitErr
}
