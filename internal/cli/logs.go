package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/config"
	"github.com/stefankn/runway/internal/destination"
	"github.com/stefankn/runway/internal/project"
	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

// logStopTimeout bounds stopping a log stream after Ctrl-C or app exit.
const logStopTimeout = 10 * time.Second

var lastRE = regexp.MustCompile(`^[0-9]+[smhd]$`)

// logFlags are the filters of `runway logs`.
type logFlags struct {
	level, category, process, predicate, last, archive string
	all, json                                          bool
}

func (f *logFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.level, "level", xcode.LevelDebug, "lowest level to show: default, info or debug")
	fl.StringVar(&f.category, "category", "", "only show this Logger category")
	fl.StringVar(&f.process, "process", "", "only show messages from this process")
	fl.StringVar(&f.predicate, "predicate", "", "raw NSPredicate `filter`, replacing the subsystem filter (see man log)")
	fl.BoolVar(&f.all, "all", false, "show everything, not just the app's subsystem")
	fl.BoolVar(&f.json, "json", false, "print the log tool's NDJSON unchanged")
	fl.StringVar(&f.last, "last", "", "show recent messages instead of streaming, e.g. 5m, 1h (required for devices)")
	fl.StringVar(&f.archive, "archive", "", "show the messages in a .logarchive, e.g. one made with sudo log collect")
	for _, other := range []string{"sim", "device", "destination", "last"} {
		cmd.MarkFlagsMutuallyExclusive("archive", other)
	}
	cmd.MarkFlagsMutuallyExclusive("all", "predicate")
	cmd.MarkFlagsMutuallyExclusive("all", "category")
	cmd.MarkFlagsMutuallyExclusive("all", "process")
	cmd.MarkFlagsMutuallyExclusive("predicate", "category")
	cmd.MarkFlagsMutuallyExclusive("predicate", "process")
}

func (f *logFlags) validate() error {
	if !slices.Contains([]string{xcode.LevelDefault, xcode.LevelInfo, xcode.LevelDebug}, f.level) {
		return fmt.Errorf("invalid --level %q: use default, info or debug", f.level)
	}
	if f.last != "" && !lastRE.MatchString(f.last) {
		return fmt.Errorf("invalid --last %q: use a number and a unit, e.g. 30s, 5m, 1h or 2d", f.last)
	}
	return nil
}

// filtersOnApp reports whether the subsystem filter (the app's) applies.
func (f *logFlags) filtersOnApp() bool { return !f.all && f.predicate == "" }

func newLogsCommand(opts *options) *cobra.Command {
	var (
		pf projectFlags
		df destFlags
		lf logFlags
	)
	cmd := &cobra.Command{
		Use:   "logs [flags]",
		Short: "Stream the app's unified log (Logger / os_log)",
		Long: `Stream the app's unified logging output (Logger, os_log) from a simulator,
like Xcode's console. By default only messages from the app's subsystem are
shown: log_subsystem from the configuration, or else the app's bundle ID.

Without --sim or --device, runway uses the booted simulator if exactly one is
booted. The app's print() output is not part of the unified log; use
runway run (or runway run --logs for both).

Physical devices can't stream their log to the command line. Use --last to
collect recent messages from a device instead (this needs sudo), or --archive
to read a .logarchive collected earlier.`,
		Example: `  runway logs
  runway logs --level info --category network
  runway logs --sim "iPhone 17" --all
  runway logs --last 10m
  runway logs --device --last 5m
  runway logs --archive device.logarchive`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runLogs(cmd, args, &pf, &df, &lf)
		},
	}
	pf.register(cmd)
	df.register(cmd)
	lf.register(cmd)
	return cmd
}

func (o *options) runLogs(cmd *cobra.Command, args []string, pf *projectFlags, df *destFlags, lf *logFlags) error {
	if err := lf.validate(); err != nil {
		return err
	}
	ctx := cmd.Context()
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	st := ui.NewStyler(stderr)

	cfg, err := o.resolveConfig(pf)
	if err != nil {
		return err
	}
	req, err := df.request(cmd, args, &cfg)
	if err != nil {
		return err
	}
	if req.Raw != "" {
		return errors.New("logs needs --sim or --device (--destination only works with build)")
	}
	req.PreferBooted = true
	client, pick := o.client(cmd), o.picker(cmd)
	var dest destination.Destination // none for --archive
	if lf.archive != "" {
		if fi, err := os.Stat(lf.archive); err != nil || !fi.IsDir() {
			return fmt.Errorf("%s is not a .logarchive", lf.archive)
		}
	} else if dest, err = destination.Resolve(ctx, client, req, pick); err != nil {
		return err
	}

	filter := xcode.LogFilter{Level: lf.level, Category: lf.category, Process: lf.process, Predicate: lf.predicate}
	if lf.filtersOnApp() {
		if filter.Subsystem, err = logSubsystem(ctx, client, &cfg, dest, pick); err != nil {
			return err
		}
	}

	printer := &logPrinter{w: stdout, st: ui.NewStyler(stdout), showProcess: !lf.filtersOnApp() || lf.process != "", raw: lf.json}
	out := printer.writer()
	defer out.Flush()
	lo := xcode.LogOutput{Stdout: out, Stderr: stderr}
	what := "all messages"
	if filter.Subsystem != "" {
		what = filter.Subsystem
	} else if filter.Predicate != "" {
		what = "messages matching the predicate"
	}

	if lf.archive != "" {
		fmt.Fprintln(stderr, st.Dim(fmt.Sprintf("Showing %s in %s", what, lf.archive)))
		return client.LogShowArchive(ctx, lf.archive, filter, lo)
	}
	if dest.Device != nil {
		return deviceLogs(ctx, client, *dest.Device, lf.last, filter, lo, st, what)
	}
	sim := *dest.Simulator
	if lf.last != "" {
		fmt.Fprintln(stderr, st.Dim(fmt.Sprintf("Logs of the last %s for %s on %s", lf.last, what, sim.Name)))
		return client.SimLogShow(ctx, sim.UDID, lf.last, filter, lo)
	}
	if !sim.Booted() {
		return fmt.Errorf("simulator %q is not booted; start the app with `runway run --sim`, or use --last to read old logs", sim.Name)
	}
	fmt.Fprintln(stderr, st.Dim(fmt.Sprintf("Streaming %s on %s (Ctrl-C to stop)", what, sim.Name)))
	s, err := client.SimLogStream(ctx, sim.UDID, filter, lo)
	if err != nil {
		return err
	}
	defer stopLogStream(ctx, s)
	select {
	case <-s.Done():
		return s.Wait()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stopLogStream stops s with a fresh, time-limited context, so it also works
// after Ctrl-C has cancelled ctx.
func stopLogStream(ctx context.Context, s *xcode.LogStream) {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logStopTimeout)
	defer cancel()
	s.Stop(stopCtx)
}

// logSubsystem returns the subsystem to filter on: log_subsystem from the
// configuration, or else the app's bundle ID from its build settings. No
// build is needed.
func logSubsystem(ctx context.Context, client *xcode.Client, cfg *config.Resolved, dest destination.Destination, pick ui.Picker) (string, error) {
	if cfg.LogSubsystem != "" {
		return cfg.LogSubsystem, nil
	}
	p, err := project.Resolve(ctx, client, cfg, pick)
	if err != nil {
		return "", fmt.Errorf("%w\n(logs filters on the app's bundle ID; set log_subsystem, or use --all or --predicate)", err)
	}
	app, err := client.AppProduct(ctx, target{project: p, dest: dest}.buildOptions(false))
	if err != nil {
		return "", err
	}
	if app.BundleID == "" {
		return "", errors.New("the app has no bundle ID (PRODUCT_BUNDLE_IDENTIFIER); set log_subsystem, or use --all or --predicate")
	}
	return app.BundleID, nil
}

// deviceLogs collects a device's recent log into a temporary archive and
// prints it. Devices can't stream their log to the command line (see
// docs/device-logs.md).
func deviceLogs(ctx context.Context, client *xcode.Client, dev xcode.Device, last string, f xcode.LogFilter,
	lo xcode.LogOutput, st ui.Styler, what string) error {
	if last == "" {
		return fmt.Errorf("logs can't be streamed live from a device; use --last 5m to read recent messages, " +
			"or `runway run --device --logs` to see them while the app runs")
	}
	dir, err := os.MkdirTemp("", "runway-logs-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	archive := filepath.Join(dir, "device.logarchive")

	fmt.Fprintln(lo.Stderr, st.Dim(fmt.Sprintf("Collecting the last %s of logs from %s…", last, dev.Name)))
	if err := client.DeviceLogCollect(ctx, dev.UDID, last, archive); err != nil {
		if ee, ok := errors.AsType[*xcode.ExitError](err); ok && needsRoot(string(ee.Stderr)+err.Error()) {
			// runway doesn't run sudo itself: asking for a password
			// in the middle of a command would be a surprise.
			fmt.Fprintln(lo.Stderr, st.Yellow("hint: ")+"log collect needs root for a device. Run:\n  "+
				xcode.Quote("sudo", "log", "collect", "--device-udid", dev.UDID, "--last", last, "--output", "device.logarchive")+
				"\n  runway logs --archive device.logarchive")
		}
		return err
	}
	fmt.Fprintln(lo.Stderr, st.Dim("Showing "+what))
	return client.LogShowArchive(ctx, archive, f, lo)
}

func needsRoot(s string) bool {
	s = strings.ToLower(s)
	// Xcode 27.2: "log: Must be root to collect logs from attached device" (exit status 77).
	return strings.Contains(s, "must be root") || strings.Contains(s, "must be run as root") ||
		strings.Contains(s, "requires root") || strings.Contains(s, "sudo")
}
