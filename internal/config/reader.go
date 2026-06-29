package config

import (
	"bytes"
	"io"
)

// newReader wraps a byte slice in an io.Reader. Used so we can decode from
// the same bytes twice (once for raw yaml.Node, once for strict decoding).
func newReader(data []byte) io.Reader {
	return bytes.NewReader(data)
}
