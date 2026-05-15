package data

import (
	"fmt"
	"io"
)

// errWriter wraps an io.Writer and captures the first write error.
// Callers must return ew.err at the end, returning nil unconditionally
// defeats the purpose of this helper.
type errWriter struct {
	w   io.Writer
	err error
}

func (ew *errWriter) write(b []byte) {
	if ew.err != nil {
		return
	}
	_, ew.err = ew.w.Write(b)
}

func (ew *errWriter) writef(format string, args ...any) {
	if ew.err != nil {
		return
	}
	_, ew.err = fmt.Fprintf(ew.w, format, args...)
}

func closeIfNotNil(name string, c io.Closer) error {
	if c == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, c.Close())
}
