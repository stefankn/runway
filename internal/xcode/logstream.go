package xcode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/stefankn/runway/internal/lines"
)

// LogFilter selects unified logging messages for `log stream` / `log show`.
type LogFilter struct {
	Level     string // LevelDefault, LevelInfo or LevelDebug (the default)
	Subsystem string
	Category  string
	Process   string
	Predicate string // raw NSPredicate; used instead of the fields above
}

// predicate returns the NSPredicate for f, or "" for no filter. A non-empty
// marker adds a term that matches every real message but makes the command
// line unique, so the process can be found again (see LogStream.Stop).
func (f LogFilter) predicate(marker string) string {
	var terms []string
	if f.Predicate != "" {
		terms = append(terms, "("+f.Predicate+")")
	} else {
		for _, t := range []struct{ key, value string }{
			{"subsystem", f.Subsystem}, {"category", f.Category}, {"process", f.Process},
		} {
			if t.value != "" {
				terms = append(terms, t.key+" == "+quotePredicate(t.value))
			}
		}
	}
	if marker != "" {
		terms = append(terms, "eventMessage != "+quotePredicate(marker))
	}
	return strings.Join(terms, " AND ")
}

// quotePredicate returns s as an NSPredicate string literal.
func quotePredicate(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (f LogFilter) level() string {
	if f.Level == "" {
		return LevelDebug
	}
	return f.Level
}

// streamArgs returns the arguments of `log stream` for f.
func (f LogFilter) streamArgs(marker string) []string {
	args := []string{"stream", "--style", "ndjson", "--level", f.level()}
	if p := f.predicate(marker); p != "" {
		args = append(args, "--predicate", p)
	}
	return args
}

// showArgs returns the arguments of `log show` for f. `log show` has flags
// for the levels to include instead of `--level`.
func (f LogFilter) showArgs() []string {
	args := []string{"show", "--style", "ndjson"}
	switch f.level() {
	case LevelDebug:
		args = append(args, "--info", "--debug")
	case LevelInfo:
		args = append(args, "--info")
	}
	if p := f.predicate(""); p != "" {
		args = append(args, "--predicate", p)
	}
	return args
}

// Output of `log stream|show --style ndjson`: one JSON object per line.
// Only the fields runway uses.
type rawLogEvent struct {
	EventType        string `json:"eventType"`
	MessageType      string `json:"messageType"`
	Timestamp        string `json:"timestamp"`
	EventMessage     string `json:"eventMessage"`
	Subsystem        string `json:"subsystem"`
	Category         string `json:"category"`
	ProcessImagePath string `json:"processImagePath"`
	ProcessID        int    `json:"processID"`
}

const logTimeLayout = "2006-01-02 15:04:05.999999-0700"

// ParseLogLine parses one line of `log stream --style ndjson` output. ok is
// false for anything that isn't a log message: other text the tool prints,
// and other events such as activityCreateEvent.
func ParseLogLine(line []byte) (e LogEntry, ok bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return LogEntry{}, false
	}
	var raw rawLogEvent
	if err := json.Unmarshal(line, &raw); err != nil || raw.EventType != "logEvent" {
		return LogEntry{}, false
	}
	t, _ := time.Parse(logTimeLayout, raw.Timestamp)
	return LogEntry{
		Time:      t,
		Level:     strings.ToLower(raw.MessageType),
		Subsystem: raw.Subsystem,
		Category:  raw.Category,
		Process:   path.Base(raw.ProcessImagePath),
		PID:       raw.ProcessID,
		Message:   raw.EventMessage,
	}, true
}

// logNoise filters lines that `log` prints to stderr on every run.
func logNoise(w io.Writer) *lines.Writer {
	return lines.New(func(l []byte) {
		s := string(l)
		if w == nil || strings.HasPrefix(s, "getpwuid_r did not find a match") || strings.HasPrefix(s, "Filtering the log data") {
			return
		}
		_, _ = io.WriteString(w, s+"\n")
	})
}

// LogOutput receives the output of a log command.
type LogOutput struct {
	Stdout io.Writer // NDJSON, one message per line (see ParseLogLine)
	Stderr io.Writer // errors; known noise is dropped
}

// Timing of LogStream.Stop; variables so tests can shorten them.
var (
	logPIDPoll     = 100 * time.Millisecond // between searches for the log process
	logPIDAttempts = 20                     // searches before giving up
	logStopGrace   = 2 * time.Second        // for simctl to exit after log has stopped
)

// newLogMarker returns a string unique to one log stream.
var newLogMarker = func() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "runway-" + hex.EncodeToString(b)
}

// LogStream is a running `log stream` on a simulator. Always call Stop.
type LogStream struct {
	c      *Client
	marker string
	cancel context.CancelFunc
	done   chan struct{}
	err    error
	stderr *lines.Writer
	pids   []int // the `log` process, once found
}

// SimLogStream streams a simulator's unified log until Stop is called.
//
// `log` runs inside the simulator, started by simctl spawn. Stopping simctl
// does not stop it (simctl ignores SIGINT and SIGTERM, and `log` survives
// even SIGKILL of simctl), so Stop signals the `log` process itself. See
// docs/xcode-notes.md.
func (c *Client) SimLogStream(ctx context.Context, udid string, f LogFilter, out LogOutput) (*LogStream, error) {
	s := &LogStream{c: c, marker: newLogMarker(), done: make(chan struct{}), stderr: logNoise(out.Stderr)}
	cmd := xcrun(append([]string{"simctl", "spawn", udid, "log"}, f.streamArgs(s.marker)...)...)
	cmd.Stdout, cmd.Stderr = out.Stdout, s.stderr
	// Ctrl-C must not interrupt simctl directly: that achieves nothing (see
	// above) and only Stop knows how to end the stream.
	pctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p, err := c.r.Start(pctx, cmd)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("streaming logs: %w", err)
	}
	s.cancel = cancel
	go func() {
		s.err = p.Wait()
		s.stderr.Flush()
		close(s.done)
	}()
	return s, nil
}

// Done is closed when the stream has ended.
func (s *LogStream) Done() <-chan struct{} { return s.done }

// Wait waits for the stream to end and returns its error. A stream ends by
// itself only on failure, e.g. a bad predicate or a simulator shutting down.
func (s *LogStream) Wait() error {
	<-s.done
	if s.err != nil {
		return fmt.Errorf("streaming logs: %w", s.err)
	}
	return nil
}

// WaitStarted waits until the `log` process is running inside the
// simulator, so that messages logged from then on are seen. It gives up
// quietly after logPIDAttempts searches; the stream may still work.
func (s *LogStream) WaitStarted(ctx context.Context) {
	s.findPIDs(ctx)
}

// findPIDs searches for the `log` process until it is found, simctl has
// exited, ctx is done or logPIDAttempts searches have failed.
func (s *LogStream) findPIDs(ctx context.Context) []int {
	for attempt := 0; len(s.pids) == 0 && attempt < logPIDAttempts; attempt++ {
		if s.pids = s.logPIDs(ctx); len(s.pids) > 0 {
			break
		}
		select {
		case <-s.done:
			return nil // simctl has exited; `log` never started
		case <-ctx.Done():
			return nil
		case <-time.After(logPIDPoll):
		}
	}
	return s.pids
}

// Stop ends the stream: it interrupts the `log` process inside the
// simulator, gives simctl logStopGrace to exit, and then interrupts and, if
// needed, kills simctl. Use a context that isn't cancelled yet (e.g. one
// derived with context.WithoutCancel after Ctrl-C). Stop must not be called
// concurrently with WaitStarted.
func (s *LogStream) Stop(ctx context.Context) {
	defer s.cancel()
	for _, pid := range s.findPIDs(ctx) {
		_, _ = s.c.r.Output(ctx, Cmd{Name: "kill", Args: []string{"-INT", strconv.Itoa(pid)}})
	}
	select {
	case <-s.done:
		return
	case <-time.After(logStopGrace):
	case <-ctx.Done():
	}
	s.cancel()
	<-s.done
}

// logPIDs finds this stream's `log` process. Simulator processes are
// visible to the host's pgrep; the marker in the predicate makes the match
// unique, and "^log stream" excludes simctl, whose command line contains the
// marker too.
func (s *LogStream) logPIDs(ctx context.Context) []int {
	out, err := s.c.r.Output(ctx, Cmd{Name: "pgrep", Args: []string{"-f", "^log stream .*" + s.marker}})
	if err != nil {
		return nil // exit status 1: no match
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// SimLogShow prints a simulator's recent unified log (`log show --last`),
// e.g. last = "5m".
func (c *Client) SimLogShow(ctx context.Context, udid, last string, f LogFilter, out LogOutput) error {
	args := append([]string{"simctl", "spawn", udid, "log"}, f.showArgs()...)
	cmd := xcrun(append(args, "--last", last)...)
	return c.runLog(ctx, cmd, out)
}

// DeviceLogCollect copies the last part of a physical device's unified log
// into a new .logarchive at archive (which must not exist yet).
func (c *Client) DeviceLogCollect(ctx context.Context, udid, last, archive string) error {
	cmd := Cmd{Name: "log", Args: []string{"collect", "--device-udid", udid, "--last", last, "--output", archive}}
	if _, err := c.r.Output(ctx, cmd); err != nil {
		return fmt.Errorf("collecting device logs: %w", err)
	}
	return nil
}

// LogShowArchive prints the messages in a .logarchive.
func (c *Client) LogShowArchive(ctx context.Context, archive string, f LogFilter, out LogOutput) error {
	return c.runLog(ctx, Cmd{Name: "log", Args: append(f.showArgs(), archive)}, out)
}

func (c *Client) runLog(ctx context.Context, cmd Cmd, out LogOutput) error {
	stderr := logNoise(out.Stderr)
	cmd.Stdout, cmd.Stderr = out.Stdout, stderr
	err := c.r.Run(ctx, cmd)
	stderr.Flush()
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("reading logs: %w", err)
	}
	return err
}
