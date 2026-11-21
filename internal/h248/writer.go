package h248

import (
	"bytes"
	"fmt"
	"strings"
)

// writer lays a message out the way Marshal writes it: one item per
// line, nested items two spaces deeper. The grammar allows white space
// around braces and commas (LWSP in RFC 3525 Annex B.2), so the layout
// is free, and this one is for people reading a capture.
type writer struct {
	buf   bytes.Buffer
	depth int
	err   error
}

// fail keeps the first reason why the message cannot be written.
func (w *writer) fail(format string, args ...any) {
	if w.err == nil {
		w.err = fmt.Errorf("h248: "+format, args...)
	}
}

func (w *writer) indent() {
	w.buf.WriteString(strings.Repeat("  ", w.depth))
}

// item writes text at the current depth. It does not end the line: the
// block the item is in knows whether a comma follows.
func (w *writer) item(text string) {
	w.indent()
	w.buf.WriteString(text)
}

// block writes "head {", the items one level deeper with commas
// between them, and "}".
func (w *writer) block(head string, items []func()) {
	w.item(head + " {")
	w.depth++
	for i, item := range items {
		if i > 0 {
			w.buf.WriteByte(',')
		}
		w.buf.WriteByte('\n')
		item()
	}
	w.depth--
	w.buf.WriteByte('\n')
	w.item("}")
}
