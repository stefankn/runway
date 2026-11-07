// Package xcode is the only package in runway that runs external processes or
// knows the arguments of Apple's tools (xcodebuild, simctl, devicectl, log).
package xcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Cmd describes one invocation of an external tool. It is always executed
// directly with an argument slice, never through a shell.
type Cmd struct {
	Name   string   // executable, e.g. "xcrun", "xcodebuild", "log"
	Args   []string // arguments, passed unchanged
	Dir    string   // working directory; empty means the current one
	Env    []string // "KEY=VALUE" entries added to the inherited environment
	Stdin  io.Reader
	Stdout io.Writer // used by Run and Start; nil discards
	Stderr io.Writer // used by Run and Start; nil discards (Output also captures it)
}

// String returns the command line, quoted so it can be pasted into a shell.
func (c Cmd) String() string {
	var b strings.Builder
	if c.Dir != "" {
		b.WriteString("cd ")
		b.WriteString(quoteArg(c.Dir))
		b.WriteString(" && ")
	}
	for _, kv := range c.Env {
		k, v, _ := strings.Cut(kv, "=")
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(quoteArg(v))
		b.WriteByte(' ')
	}
	b.WriteString(Quote(append([]string{c.Name}, c.Args...)...))
	return b.String()
}

// Runner runs external commands. Production code uses ExecRunner; tests use FakeRunner.
type Runner interface {
	// Output runs the command and returns its stdout. Used for JSON queries.
	Output(ctx context.Context, c Cmd) ([]byte, error)
	// Run runs the command, streaming to c.Stdout and c.Stderr, and waits for it.
	Run(ctx context.Context, c Cmd) error
	// Start starts a long-running command (console, log stream) and returns
	// immediately. Cancelling ctx interrupts the process.
	Start(ctx context.Context, c Cmd) (Process, error)
}

// Process is a command started by Runner.Start.
type Process interface {
	Wait() error
	Signal(os.Signal) error
}

// ExitError reports that a command ran but exited unsuccessfully.
type ExitError struct {
	Cmd    Cmd
	Code   int    // exit status; -1 if the process was killed by a signal
	Stderr []byte // captured stderr (Output only)
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s exited with status %d", e.Cmd.Name, e.Code)
	if s := strings.TrimSpace(string(e.Stderr)); s != "" {
		msg += ": " + s
	}
	return msg
}

// ExitCode lets callers propagate the child's exit status.
func (e *ExitError) ExitCode() int { return e.Code }

// DefaultWaitDelay is how long a cancelled command has to exit after SIGINT
// before it is killed.
const DefaultWaitDelay = 3 * time.Second

// ExecRunner runs commands with os/exec.
type ExecRunner struct {
	Verbose   bool          // echo every command before it runs
	Log       io.Writer     // destination for the echo; nil means os.Stderr
	WaitDelay time.Duration // zero means DefaultWaitDelay
}

var (
	_ Runner = (*ExecRunner)(nil)
	_ Runner = (*FakeRunner)(nil)
)

func (r *ExecRunner) command(ctx context.Context, c Cmd) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	// Interrupt like Ctrl-C would, so tools can clean up; kill after WaitDelay.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = r.WaitDelay
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = DefaultWaitDelay
	}
	r.echo(c)
	return cmd
}

func (r *ExecRunner) echo(c Cmd) {
	if !r.Verbose {
		return
	}
	w := r.Log
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "$ %s\n", c)
}

// Output implements Runner.
func (r *ExecRunner) Output(ctx context.Context, c Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	if c.Stderr != nil {
		c.Stderr = io.MultiWriter(&stderr, c.Stderr)
	} else {
		c.Stderr = &stderr
	}
	err := r.command(ctx, c).Run()
	return stdout.Bytes(), wrapErr(ctx, c, err, stderr.Bytes())
}

// Run implements Runner.
func (r *ExecRunner) Run(ctx context.Context, c Cmd) error {
	return wrapErr(ctx, c, r.command(ctx, c).Run(), nil)
}

// Start implements Runner.
func (r *ExecRunner) Start(ctx context.Context, c Cmd) (Process, error) {
	cmd := r.command(ctx, c)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProcess{ctx: ctx, c: c, cmd: cmd}, nil
}

type execProcess struct {
	ctx context.Context
	c   Cmd
	cmd *exec.Cmd
}

func (p *execProcess) Wait() error              { return wrapErr(p.ctx, p.c, p.cmd.Wait(), nil) }
func (p *execProcess) Signal(s os.Signal) error { return p.cmd.Process.Signal(s) }

// wrapErr turns os/exec errors into runway's: cancellation is reported as the
// context error, a failed exit as *ExitError. Other errors (e.g. executable not
// found) pass through unchanged.
func wrapErr(ctx context.Context, c Cmd, err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", c.Name, ctxErr)
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return &ExitError{Cmd: c, Code: ee.ExitCode(), Stderr: stderr}
	}
	return err
}

// Quote joins args into a POSIX shell command line, quoting where needed.
func Quote(args ...string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = quoteArg(a)
	}
	return strings.Join(quoted, " ")
}

func quoteArg(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool { return !isSafe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isSafe(r rune) bool {
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' ||
		strings.ContainsRune("_@%+=:,./-", r)
}
