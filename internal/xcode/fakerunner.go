package xcode

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
)

// FakeResult is the scripted outcome of a command run by FakeRunner.
type FakeResult struct {
	Stdout   []byte // returned by Output; written to Cmd.Stdout by Run and Start
	Stderr   []byte // written to Cmd.Stderr
	Err      error  // returned by Output/Run, or by Process.Wait for Start
	StartErr error  // Start only: fail to start
	Block    bool   // Start only: run until FakeProcess.Exit is called or ctx is done
	// EchoStdin (Start only) copies Cmd.Stdin to Cmd.Stdout, like `cat`.
	// Without it, stdin is read and discarded. Either way a non-blocking
	// process exits once stdin reaches EOF.
	EchoStdin bool
}

// FakeRunner is a Runner for tests. It records every command and returns
// results scripted with Respond. It is safe for concurrent use.
type FakeRunner struct {
	Default FakeResult        // used for commands with no scripted result
	Paths   map[string]string // executables LookPath finds, name → path
	// Handler, if set, is asked first; it returns false to fall back to the
	// results scripted with Respond and then Default.
	Handler func(Cmd) (FakeResult, bool)

	mu        sync.Mutex
	calls     []Cmd
	results   map[string]FakeResult
	processes []*FakeProcess
}

// Respond scripts the result for the exact command name + args.
func (f *FakeRunner) Respond(r FakeResult, name string, args ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.results == nil {
		f.results = map[string]FakeResult{}
	}
	f.results[Quote(append([]string{name}, args...)...)] = r
}

// Calls returns the commands run so far, in order.
func (f *FakeRunner) Calls() []Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Cmd(nil), f.calls...)
}

// Processes returns the processes started so far, in order.
func (f *FakeRunner) Processes() []*FakeProcess {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*FakeProcess(nil), f.processes...)
}

func (f *FakeRunner) record(c Cmd) FakeResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if f.Handler != nil {
		if r, ok := f.Handler(c); ok {
			return r
		}
	}
	if r, ok := f.results[Quote(append([]string{c.Name}, c.Args...)...)]; ok {
		return r
	}
	return f.Default
}

// Output implements Runner.
func (f *FakeRunner) Output(_ context.Context, c Cmd) ([]byte, error) {
	r := f.record(c)
	writeTo(c.Stderr, r.Stderr)
	return r.Stdout, r.Err
}

// Run implements Runner.
func (f *FakeRunner) Run(_ context.Context, c Cmd) error {
	r := f.record(c)
	writeTo(c.Stdout, r.Stdout)
	writeTo(c.Stderr, r.Stderr)
	return r.Err
}

// Start implements Runner.
func (f *FakeRunner) Start(ctx context.Context, c Cmd) (Process, error) {
	r := f.record(c)
	if r.StartErr != nil {
		return nil, r.StartErr
	}
	p := &FakeProcess{Cmd: c, done: make(chan struct{})}
	f.mu.Lock()
	f.processes = append(f.processes, p)
	f.mu.Unlock()

	writeTo(c.Stdout, r.Stdout)
	writeTo(c.Stderr, r.Stderr)
	if c.Stdin != nil && !r.Block {
		go func() {
			dst := io.Discard
			if r.EchoStdin && c.Stdout != nil {
				dst = c.Stdout
			}
			_, _ = io.Copy(dst, c.Stdin)
			p.Exit(r.Err)
		}()
		return p, nil
	}
	if !r.Block {
		p.Exit(r.Err)
		return p, nil
	}
	go func() {
		select {
		case <-ctx.Done():
			p.Exit(ctx.Err())
		case <-p.done:
		}
	}()
	return p, nil
}

// LookPath implements Runner, using Paths.
func (f *FakeRunner) LookPath(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.Paths[name]; ok {
		return p, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func writeTo(w interface{ Write([]byte) (int, error) }, b []byte) {
	if w != nil && len(b) > 0 {
		_, _ = w.Write(b)
	}
}

// FakeProcess is a Process controlled by the test.
type FakeProcess struct {
	Cmd Cmd

	done    chan struct{}
	once    sync.Once
	err     error
	mu      sync.Mutex
	signals []os.Signal
}

// Exit makes Wait return err. Calls after the first are ignored.
func (p *FakeProcess) Exit(err error) {
	p.once.Do(func() {
		p.err = err
		close(p.done)
	})
}

// Wait implements Process.
func (p *FakeProcess) Wait() error {
	<-p.done
	return p.err
}

// Signal implements Process. It records the signal; it does not exit.
func (p *FakeProcess) Signal(s os.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.signals = append(p.signals, s)
	return nil
}

// Signals returns the signals sent so far.
func (p *FakeProcess) Signals() []os.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]os.Signal(nil), p.signals...)
}
