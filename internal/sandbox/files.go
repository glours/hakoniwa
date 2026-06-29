package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HakoOutPath returns the sandbox path for a channel's output payload.
// Convention: /<workspace>/.hako/out/<channel>.json
// In v0 the workspace root is /root (default for root-user containers).
const hakoWorkspace = "/root"

func HakoOutPath(channel string) string {
	return hakoWorkspace + "/.hako/out/" + channel + ".json"
}

// HakoInPath returns the sandbox path for a channel's input payload.
// Convention: /<workspace>/.hako/in/<channel>.json
func HakoInPath(channel string) string {
	return hakoWorkspace + "/.hako/in/" + channel + ".json"
}

// FileStager provides the .hako/ payload operations used by the orchestrator.
// Implemented by *DaemonClient; separated from Client for mockability.
type FileStager interface {
	// GetFile reads the file at path inside sandbox name and returns its
	// contents. Returns *NotFoundError when the sandbox or path is absent.
	GetFile(ctx context.Context, name, path string) ([]byte, error)

	// PutFile writes data to path inside sandbox name, creating any
	// intermediate directories. Overwrites any existing file.
	PutFile(ctx context.Context, name, path string, data []byte) error
}

// Verify at compile time.
var _ FileStager = (*DaemonClient)(nil)

// GetFile reads a single file from the sandbox via GET /sandbox/{name}/files.
//
// The daemon returns the file as a tar archive (docker-cp semantics).
// GetFile extracts the first regular entry and returns its content.
//
// Returns *NotFoundError for HTTP 404 (sandbox or path missing) and
// a plain error for HTTP 409 (sandbox not running) or 5xx.
func (c *DaemonClient) GetFile(ctx context.Context, name, path string) ([]byte, error) {
	req, err := c.newFileRequest(ctx, http.MethodGet, name, path, nil, "")
	if err != nil {
		return nil, fmt.Errorf("GetFile %q %q: build request: %w", name, path, err)
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return c.dial(ctx)
			},
		},
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GetFile %q %q: %w", name, path, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through to tar extraction
	case http.StatusNotFound:
		return nil, &NotFoundError{Resource: fmt.Sprintf("sandbox %s path %s", name, path)}
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GetFile %q %q: unexpected status %d: %s",
			name, path, resp.StatusCode, extractErrorMessage(body))
	}

	data, err := extractTarEntry(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GetFile %q %q: extract tar: %w", name, path, err)
	}
	return data, nil
}

// PutFile writes data to path inside the sandbox via PUT /sandbox/{name}/files.
//
// It wraps data in a tar archive (docker-cp semantics) and streams it to the
// daemon. Intermediate directories are created by the daemon's tar extraction.
//
// Returns *NotFoundError for HTTP 404, and a plain error for 409 (not running)
// or 5xx.
func (c *DaemonClient) PutFile(ctx context.Context, name, path string, data []byte) error {
	// Build the tar archive. The extraction root (path query param) must be
	// the directory; the archive entry name is the base filename.
	dir, base := splitPath(path)
	tarData, err := buildTarEntry(base, data)
	if err != nil {
		return fmt.Errorf("PutFile %q %q: build tar: %w", name, path, err)
	}

	req, err := c.newFileRequest(ctx, http.MethodPut, name, dir, bytes.NewReader(tarData), "application/x-tar")
	if err != nil {
		return fmt.Errorf("PutFile %q %q: build request: %w", name, path, err)
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return c.dial(ctx)
			},
		},
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("PutFile %q %q: %w", name, path, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return &NotFoundError{Resource: "sandbox " + name}
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PutFile %q %q: unexpected status %d: %s",
			name, path, resp.StatusCode, extractErrorMessage(body))
	}
}

// ReadChannelPayload reads the emitter's output payload for channel ch from
// sandbox sbxName. Convenience wrapper around GetFile.
func (c *DaemonClient) ReadChannelPayload(ctx context.Context, sbxName, ch string) (json.RawMessage, error) {
	data, err := c.GetFile(ctx, sbxName, HakoOutPath(ch))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// StageChannelPayload writes the payload for channel ch into the subscriber's
// sandbox at the well-known .hako/in/<channel>.json path. Convenience wrapper
// around PutFile.
func (c *DaemonClient) StageChannelPayload(ctx context.Context, sbxName, ch string, payload json.RawMessage) error {
	return c.PutFile(ctx, sbxName, HakoInPath(ch), []byte(payload))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newFileRequest builds an *http.Request for GET or PUT /sandbox/{name}/files
// with the path as a query parameter. For GET, body and contentType are nil/"".
func (c *DaemonClient) newFileRequest(
	ctx context.Context,
	method, name, path string,
	body io.Reader,
	contentType string,
) (*http.Request, error) {
	u, err := buildURL(c.baseURL, fmt.Sprintf("/sandbox/%s/files", name))
	if err != nil {
		return nil, err
	}
	// Add path as a query parameter.
	u += "?" + url.Values{"path": {path}}.Encode()

	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

// extractTarEntry reads a tar archive from r and returns the content of the
// first regular file entry it finds.
func extractTarEntry(r io.Reader) ([]byte, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("empty or directory-only tar archive")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg || hdr.Typeflag == 0 {
			return io.ReadAll(tr)
		}
		// Skip non-regular entries (directories, symlinks, etc.).
	}
}

// buildTarEntry creates a minimal tar archive containing a single file named
// entryName with the given content.
func buildTarEntry(entryName string, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name:     entryName,
		Size:     int64(len(data)),
		Mode:     0o644,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	if _, err := tw.Write(data); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// splitPath splits an absolute path like "/a/b/c.json" into dir="/a/b"
// and base="c.json". If path has no separator, dir="." base=path.
func splitPath(path string) (dir, base string) {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i], path[i+1:]
	}
	return ".", path
}
