package orchestrator

import (
	"fmt"
	"io"
)

// logf writes a formatted progress line to w, intentionally discarding any
// write error. All output in the orchestrator is best-effort logging to an
// io.Writer (typically os.Stdout); write failures must not shadow real errors
// returned by the operation itself.
func logf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
