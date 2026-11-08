package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	linesplit "github.com/stefankn/runway/internal/lines"
	"github.com/stefankn/runway/internal/ui"
	"github.com/stefankn/runway/internal/xcode"
)

// logPrinter turns `log --style ndjson` output into readable lines:
//
//	09:47:03.458 error   [network] request failed
//
// With raw set, JSON lines are passed through unchanged instead.
type logPrinter struct {
	w           io.Writer
	st          ui.Styler
	prefix      string // printed before every line, e.g. for run --logs
	showProcess bool   // include the process name (when not filtering on one app)
	raw         bool   // --json
}

// writer returns an io.Writer for the log tool's stdout. Call Flush on it
// when the tool has exited.
func (p *logPrinter) writer() *linesplit.Writer {
	return linesplit.New(p.line)
}

func (p *logPrinter) line(l []byte) {
	if p.raw {
		if t := bytes.TrimSpace(l); len(t) > 0 && t[0] == '{' && json.Valid(t) {
			_, _ = fmt.Fprintf(p.w, "%s%s\n", p.prefix, t)
		}
		return
	}
	if e, ok := xcode.ParseLogLine(l); ok {
		_, _ = io.WriteString(p.w, p.format(e))
	}
}

const levelWidth = len(xcode.LevelDefault)

// format renders one entry, ending in a newline. Continuation lines of a
// multi-line message are indented to the message column.
func (p *logPrinter) format(e xcode.LogEntry) string {
	head := e.Time.Format("15:04:05.000") + " " + fmt.Sprintf("%-*s", levelWidth, e.Level) + " "
	styledHead := p.st.Dim(e.Time.Format("15:04:05.000")) + " " + p.styleLevel(e.Level, fmt.Sprintf("%-*s", levelWidth, e.Level)) + " "
	if p.showProcess && e.Process != "" {
		head += e.Process + " "
		styledHead += e.Process + " "
	}
	if e.Category != "" {
		head += "[" + e.Category + "] "
		styledHead += p.st.Dim("["+e.Category+"]") + " "
	}

	msg := strings.Split(strings.TrimRight(e.Message, "\n"), "\n")
	var b strings.Builder
	indent := strings.Repeat(" ", len(head))
	for i, m := range msg {
		b.WriteString(p.prefix)
		if i == 0 {
			b.WriteString(styledHead)
		} else {
			b.WriteString(indent)
		}
		b.WriteString(p.styleMessage(e.Level, m))
		b.WriteByte('\n')
	}
	return b.String()
}

func (p *logPrinter) styleLevel(level, text string) string {
	switch level {
	case xcode.LevelDebug:
		return p.st.Dim(text)
	case xcode.LevelError:
		return p.st.Yellow(text)
	case xcode.LevelFault:
		return p.st.Red(text)
	}
	return text
}

func (p *logPrinter) styleMessage(level, text string) string {
	if level == xcode.LevelDebug {
		return p.st.Dim(text)
	}
	return text
}

// syncWriter serializes writes from several goroutines (the app's console
// and its log stream), so their lines don't break into each other.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
