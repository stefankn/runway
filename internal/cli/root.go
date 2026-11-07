// Package cli defines runway's cobra commands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

// BuildInfo describes the running binary. It is injected from main.
type BuildInfo struct {
	Version, Commit, Date string
}

func (b BuildInfo) String() string {
	return fmt.Sprintf("runway %s (commit %s, built %s)", b.Version, b.Commit, b.Date)
}

// ExitCoder is implemented by errors that carry a process exit status,
// such as *xcode.ExitError. Execute uses it to propagate exit codes.
type ExitCoder interface {
	error
	ExitCode() int
}

// options holds global flags shared by all subcommands.
type options struct {
	verbose bool

	// Test hooks.
	testRunner xcode.Runner // replaces the real runner
	testPicker ui.Picker    // replaces the interactive picker
	workDir    string       // replaces the process working directory
}

// cwd returns the directory runway works in.
func (o *options) cwd() (string, error) {
	if o.workDir != "" {
		return o.workDir, nil
	}
	return os.Getwd()
}

// picker returns the interactive picker, or nil when runway can't prompt
// (stdin or stderr is not a terminal).
func (o *options) picker(cmd *cobra.Command) ui.Picker {
	if o.testPicker != nil {
		return o.testPicker
	}
	if !ui.Interactive(cmd.InOrStdin(), cmd.ErrOrStderr()) {
		return nil
	}
	return ui.HuhPicker(cmd.InOrStdin(), cmd.ErrOrStderr())
}

// runner returns the Runner subcommands use to call Apple's tools.
// Verbose output (the echoed commands) goes to stderr.
func (o *options) runner(stderr io.Writer) xcode.Runner {
	if o.testRunner != nil {
		return o.testRunner
	}
	return &xcode.ExecRunner{Verbose: o.verbose, Log: stderr}
}

// client returns an xcode.Client for cmd, echoing commands to its stderr.
func (o *options) client(cmd *cobra.Command) *xcode.Client {
	return xcode.New(o.runner(cmd.ErrOrStderr()))
}

// NewRootCommand builds a fresh command tree.
func NewRootCommand(info BuildInfo) *cobra.Command {
	return newRootCommand(info, &options{})
}

func newRootCommand(info BuildInfo, opts *options) *cobra.Command {
	root := &cobra.Command{
		Use:   "runway",
		Short: "Build, run and stream logs for Xcode apps from the terminal",
		Long: `runway does from the terminal what Xcode's toolbar does: build, run with
live console output, stream logs, and list devices and simulators.

It is a thin layer over xcodebuild, simctl and devicectl. Use --verbose to
see every underlying command.`,
		Version:       info.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate(info.String() + "\n")
	root.PersistentFlags().BoolVarP(&opts.verbose, "verbose", "v", false, "print every underlying command before it runs")

	root.AddCommand(
		newBuildCommand(opts),
		newConfigCommand(opts),
		newDevicesCommand(opts),
		newInitCommand(opts),
		newRunCommand(opts),
		newSimulatorsCommand(opts),
		newVersionCommand(info),
	)
	return root
}

// exitInterrupted is the conventional exit status after Ctrl-C (128 + SIGINT).
const exitInterrupted = 130

// Execute runs runway with the process arguments and returns the exit code.
//
// The first SIGINT/SIGTERM cancels the command's context, which interrupts
// child processes (see xcode.ExecRunner) so they can clean up. After that the
// default signal handling is restored, so a second Ctrl-C exits immediately.
func Execute(info BuildInfo) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()
	return execute(ctx, info, os.Args[1:], os.Stdout, os.Stderr)
}

func execute(ctx context.Context, info BuildInfo, args []string, stdout, stderr io.Writer) int {
	root := NewRootCommand(info)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		fmt.Fprintln(stderr, "runway: interrupted")
		return exitInterrupted
	}
	fmt.Fprintf(stderr, "runway: %v\n", err)
	if ec, ok := errors.AsType[ExitCoder](err); ok && ec.ExitCode() > 0 {
		return ec.ExitCode()
	}
	return 1
}
