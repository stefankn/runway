// Package cli defines runway's cobra commands.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

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

	testRunner xcode.Runner // set by tests to replace the real runner
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
		newDevicesCommand(opts),
		newSimulatorsCommand(opts),
		newVersionCommand(info),
	)
	return root
}

// Execute runs runway with the process arguments and returns the exit code.
func Execute(info BuildInfo) int {
	return execute(info, os.Args[1:], os.Stdout, os.Stderr)
}

func execute(info BuildInfo, args []string, stdout, stderr io.Writer) int {
	root := NewRootCommand(info)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(stderr, "runway: %v\n", err)
	if ec, ok := errors.AsType[ExitCoder](err); ok && ec.ExitCode() > 0 {
		return ec.ExitCode()
	}
	return 1
}
