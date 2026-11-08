// Package lines splits a byte stream into lines as it arrives.
package lines

import (
	"bytes"
	"sync"
)

// Writer is an io.Writer that calls Fn once for every complete line written
// to it, without the trailing newline (or "\r\n"). A partial line is kept
// until its newline arrives or Flush is called. It is safe for concurrent use.
type Writer struct {
	Fn func(line []byte)

	mu  sync.Mutex
	buf []byte
}

// New returns a Writer that calls fn for every line.
func New(fn func(line []byte)) *Writer { return &Writer{Fn: fn} }

// Write implements io.Writer.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.Fn(bytes.TrimSuffix(w.buf[:i], []byte("\r")))
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) == 0 {
		w.buf = nil // let a large drained buffer be collected
	}
	return len(p), nil
}

// Flush passes on a final line that has no newline.
func (w *Writer) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.Fn(w.buf)
		w.buf = nil
	}
}
