package sandbox

import (
	"bytes"
	"io"
	"testing"
)

func TestDemuxStdcopyStdout(t *testing.T) {
	var buf bytes.Buffer
	_ = writeStdcopyFrame(&buf, stdcopyStdout, []byte("hello"))

	var out, errBuf bytes.Buffer
	if err := demuxStdcopy(&buf, &out, &errBuf); err != nil {
		t.Fatalf("demuxStdcopy: %v", err)
	}
	if out.String() != "hello" {
		t.Errorf("stdout = %q, want %q", out.String(), "hello")
	}
	if errBuf.Len() != 0 {
		t.Errorf("stderr unexpectedly non-empty: %q", errBuf.String())
	}
}

func TestDemuxStdcopyStderr(t *testing.T) {
	var buf bytes.Buffer
	_ = writeStdcopyFrame(&buf, stdcopyStderr, []byte("oops"))

	var out, errBuf bytes.Buffer
	if err := demuxStdcopy(&buf, &out, &errBuf); err != nil {
		t.Fatalf("demuxStdcopy: %v", err)
	}
	if errBuf.String() != "oops" {
		t.Errorf("stderr = %q, want %q", errBuf.String(), "oops")
	}
}

func TestDemuxStdcopyMultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	_ = writeStdcopyFrame(&buf, stdcopyStdout, []byte("line1\n"))
	_ = writeStdcopyFrame(&buf, stdcopyStderr, []byte("warn\n"))
	_ = writeStdcopyFrame(&buf, stdcopyStdout, []byte("line2\n"))

	var out, errBuf bytes.Buffer
	if err := demuxStdcopy(&buf, &out, &errBuf); err != nil {
		t.Fatalf("demuxStdcopy: %v", err)
	}
	if out.String() != "line1\nline2\n" {
		t.Errorf("stdout = %q", out.String())
	}
	if errBuf.String() != "warn\n" {
		t.Errorf("stderr = %q", errBuf.String())
	}
}

func TestDemuxStdcopyStdinDiscarded(t *testing.T) {
	var buf bytes.Buffer
	_ = writeStdcopyFrame(&buf, stdcopyStdin, []byte("ignored"))
	_ = writeStdcopyFrame(&buf, stdcopyStdout, []byte("kept"))

	var out, errBuf bytes.Buffer
	if err := demuxStdcopy(&buf, &out, &errBuf); err != nil {
		t.Fatalf("demuxStdcopy: %v", err)
	}
	if out.String() != "kept" {
		t.Errorf("stdout = %q, want %q", out.String(), "kept")
	}
}

func TestDemuxStdcopyEmptyPayload(t *testing.T) {
	var buf bytes.Buffer
	_ = writeStdcopyFrame(&buf, stdcopyStdout, []byte{})

	var out, errBuf bytes.Buffer
	if err := demuxStdcopy(&buf, &out, &errBuf); err != nil {
		t.Fatalf("demuxStdcopy with zero-len payload: %v", err)
	}
}

func TestDemuxStdcopyUnknownStreamType(t *testing.T) {
	var buf bytes.Buffer
	_ = writeStdcopyFrame(&buf, stdcopyStreamType(5), []byte("bad"))

	var out, errBuf bytes.Buffer
	if err := demuxStdcopy(&buf, &out, &errBuf); err == nil {
		t.Error("expected error for unknown stream type")
	}
}

func TestDemuxStdcopyTruncatedHeader(t *testing.T) {
	// Write a partial header (< 8 bytes).
	var buf bytes.Buffer
	buf.Write([]byte{1, 0, 0, 0}) // only 4 bytes

	var out, errBuf bytes.Buffer
	// Should return nil (treated as EOF/session-end) or an error — not hang.
	err := demuxStdcopy(&buf, &out, &errBuf)
	// io.ErrUnexpectedEOF is converted to nil (session end) by the demuxer.
	if err != nil {
		t.Errorf("truncated header: expected nil (session end), got: %v", err)
	}
}

func TestDemuxStdcopyLargePayload(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 65536)
	var buf bytes.Buffer
	_ = writeStdcopyFrame(&buf, stdcopyStdout, payload)

	var out bytes.Buffer
	if err := demuxStdcopy(&buf, &out, io.Discard); err != nil {
		t.Fatalf("large payload: %v", err)
	}
	if out.Len() != 65536 {
		t.Errorf("stdout len = %d, want 65536", out.Len())
	}
}
