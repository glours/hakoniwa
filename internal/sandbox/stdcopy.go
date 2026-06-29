package sandbox

import (
	"encoding/binary"
	"fmt"
	"io"
)

// stdcopyStreamType identifies the stream in a stdcopy frame header.
// Values match the Docker/Moby stdcopy framing: 0=stdin, 1=stdout, 2=stderr.
type stdcopyStreamType byte

const (
	stdcopyStdin  stdcopyStreamType = 0
	stdcopyStdout stdcopyStreamType = 1
	stdcopyStderr stdcopyStreamType = 2

	// stdcopyHeaderLen is the fixed header size: 1 byte type + 3 bytes zero + 4 bytes length.
	stdcopyHeaderLen = 8
)

// demuxStdcopy reads the stdcopy-framed non-TTY stream from r, routing each
// frame's payload to stdout (stream type 1) or stderr (stream type 2).
// Stdin frames (type 0) are silently discarded. Unknown types are an error.
//
// Returns nil when r reaches EOF (i.e. the session ended cleanly). Any other
// read error is returned directly.
//
// Wire format per frame:
//
//	[stream_type(1)][zero(1)][zero(1)][zero(1)][payload_len(4 big-endian)]
//	[payload(payload_len bytes)]
func demuxStdcopy(r io.Reader, stdout, stderr io.Writer) error {
	hdr := make([]byte, stdcopyHeaderLen)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				// Server closed the connection — session complete.
				return nil
			}
			return fmt.Errorf("demuxStdcopy: read header: %w", err)
		}

		streamType := stdcopyStreamType(hdr[0])
		payloadLen := binary.BigEndian.Uint32(hdr[4:8])

		var dst io.Writer
		switch streamType {
		case stdcopyStdin:
			dst = io.Discard
		case stdcopyStdout:
			dst = stdout
		case stdcopyStderr:
			dst = stderr
		default:
			return fmt.Errorf("demuxStdcopy: unknown stream type %d", streamType)
		}

		if payloadLen == 0 {
			continue
		}

		if _, err := io.CopyN(dst, r, int64(payloadLen)); err != nil {
			if err == io.EOF {
				// Truncated frame — treat as session end.
				return nil
			}
			return fmt.Errorf("demuxStdcopy: read payload (stream=%d, len=%d): %w",
				streamType, payloadLen, err)
		}
	}
}

// writeStdcopyFrame writes a single stdcopy frame to w (for test use).
func writeStdcopyFrame(w io.Writer, streamType stdcopyStreamType, payload []byte) error {
	hdr := make([]byte, stdcopyHeaderLen)
	hdr[0] = byte(streamType)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
