package logger

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// ANSI color codes for terminal formatting
const (
	ColorReset  = "\033[0m"
	ColorBlue   = "\033[36m" // Cyan-ish blue for standard app logs
	ColorRed    = "\033[31m" // Red for app errors
	ColorYellow = "\033[33m" // Yellow for hotreload system logs
)

// PrefixWriter is a custom io.Writer that intercepts a byte stream,
// buffers it until a newline is found, and prepends a formatted prefix.
type PrefixWriter struct {
	prefix string
	color  string
	out    io.Writer
	buf    bytes.Buffer
	mu     sync.Mutex
}

// NewPrefixWriter creates a new stream interceptor.
func NewPrefixWriter(prefix, color string, out io.Writer) *PrefixWriter {
	return &PrefixWriter{
		prefix: prefix,
		color:  color,
		out:    out,
	}
}

// Write implements the io.Writer interface.
// This is called automatically by exec.Cmd whenever the child process prints something.
func (w *PrefixWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, b := range p {
		if b == '\n' {
			// We hit a newline, flush the buffer with our custom prefix
			fmt.Fprintf(w.out, "%s[%s]%s %s\n", w.color, w.prefix, ColorReset, w.buf.String())
			w.buf.Reset()
		} else {
			// Still reading the line, store the byte
			w.buf.WriteByte(b)
		}
	}
	
	// We must return len(p) to satisfy the io.Writer interface, 
	// telling the OS we successfully consumed the bytes.
	return len(p), nil
}