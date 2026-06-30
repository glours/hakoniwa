package sandbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// upgradeHandler is an http.Handler that:
//   - validates the upgrade headers and JSON body per attachAgentSession spec
//   - responds 101 + Sandboxes-Exec-Id
//   - writes stdcopy frames then closes the conn
type upgradeHandler struct {
	t            *testing.T
	execID       string
	stdoutFrames []string
	stderrFrames []string
	// Set to a non-101 status code to exercise error paths.
	errorStatus  int
	errorMessage string
}

func (h *upgradeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Error-path handler: respond with a non-101 status before upgrade.
	if h.errorStatus != 0 {
		body, _ := json.Marshal(sandboxapi.ErrorResponse{Message: h.errorMessage})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(h.errorStatus)
		_, _ = w.Write(body)
		return
	}

	// Validate upgrade headers.
	if !strings.EqualFold(r.Header.Get("Connection"), "Upgrade") {
		h.t.Errorf("missing Connection: Upgrade, got %q", r.Header.Get("Connection"))
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "tcp") {
		h.t.Errorf("missing Upgrade: tcp, got %q", r.Header.Get("Upgrade"))
	}

	// Validate Content-Type and body.
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		h.t.Errorf("Content-Type = %q, want application/json", ct)
	}

	// Hijack the connection.
	hj, ok := w.(http.Hijacker)
	if !ok {
		h.t.Fatal("ResponseWriter does not implement http.Hijacker")
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		h.t.Fatalf("Hijack: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()

	// Write 101 Switching Protocols response.
	resp := fmt.Sprintf(
		"HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n%s: %s\r\n\r\n",
		execIDHeader, h.execID,
	)
	if _, err := buf.WriteString(resp); err != nil {
		h.t.Errorf("write 101: %v", err)
		return
	}

	// Write stdcopy frames.
	for _, s := range h.stdoutFrames {
		if err := writeStdcopyFrame(buf, stdcopyStdout, []byte(s)); err != nil {
			h.t.Errorf("write stdout frame: %v", err)
			return
		}
	}
	for _, s := range h.stderrFrames {
		if err := writeStdcopyFrame(buf, stdcopyStderr, []byte(s)); err != nil {
			h.t.Errorf("write stderr frame: %v", err)
			return
		}
	}
	if err := buf.Flush(); err != nil {
		h.t.Errorf("flush: %v", err)
	}
	// Close — session complete.
}

// newUpgradeTestClient builds a DaemonClient wired to a test server running the
// given handler.  The server is registered for cleanup by t.Cleanup.
func newUpgradeTestClient(t *testing.T, h http.Handler) (*DaemonClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewDaemonClientForURL(srv.URL)
	if err != nil {
		t.Fatalf("NewDaemonClientForURL: %v", err)
	}
	return c, srv
}

// ---------------------------------------------------------------------------
// Tests: success path
// ---------------------------------------------------------------------------

func TestAttachAgentSessionExecID(t *testing.T) {
	h := &upgradeHandler{
		t:      t,
		execID: "exec-abc-123",
	}
	client, _ := newUpgradeTestClient(t, h)

	session, err := client.AttachAgentSession(t.Context(), "my-sandbox",
		sandboxapi.AgentSessionRequest{})
	if err != nil {
		t.Fatalf("AttachAgentSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	if session.ExecID() != "exec-abc-123" {
		t.Errorf("ExecID = %q, want %q", session.ExecID(), "exec-abc-123")
	}
}

func TestAttachAgentSessionStreamOutput(t *testing.T) {
	h := &upgradeHandler{
		t:            t,
		execID:       "exec-xyz",
		stdoutFrames: []string{"hello ", "world\n"},
		stderrFrames: []string{"warn\n"},
	}
	client, _ := newUpgradeTestClient(t, h)

	session, err := client.AttachAgentSession(t.Context(), "sb",
		sandboxapi.AgentSessionRequest{})
	if err != nil {
		t.Fatalf("AttachAgentSession: %v", err)
	}
	defer func() { _ = session.Close() }()

	var out, errBuf bytes.Buffer
	if err := session.Stream(&out, &errBuf); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if out.String() != "hello world\n" {
		t.Errorf("stdout = %q, want %q", out.String(), "hello world\n")
	}
	if errBuf.String() != "warn\n" {
		t.Errorf("stderr = %q, want %q", errBuf.String(), "warn\n")
	}
}

func TestAttachAgentSessionCompletesOnEOF(t *testing.T) {
	// No frames — server closes immediately after 101.
	h := &upgradeHandler{t: t, execID: "exec-done"}
	client, _ := newUpgradeTestClient(t, h)

	session, err := client.AttachAgentSession(t.Context(), "sb",
		sandboxapi.AgentSessionRequest{})
	if err != nil {
		t.Fatalf("AttachAgentSession: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- session.Stream(io.Discard, io.Discard)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Stream: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("Stream did not return after server closed")
	}
}

func TestAttachAgentSessionBodyForwarded(t *testing.T) {
	// Validate that args and env are forwarded in the JSON body.
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		hj := w.(http.Hijacker)
		conn, buf, _ := hj.Hijack()
		defer func() { _ = conn.Close() }()
		resp := fmt.Sprintf(
			"HTTP/1.1 101 Switching Protocols\r\nUpgrade: tcp\r\nConnection: Upgrade\r\n%s: exec-1\r\n\r\n",
			execIDHeader,
		)
		_, _ = buf.WriteString(resp)
		_ = buf.Flush()
	}))
	defer srv.Close()

	client, _ := NewDaemonClientForURL(srv.URL)
	args := []string{"--prompt", "hello world"}
	env := map[string]string{"KEY": "val"}
	tty := false
	req := sandboxapi.AgentSessionRequest{Args: &args, Env: &env, Tty: &tty}

	session, err := client.AttachAgentSession(t.Context(), "sb", req)
	if err != nil {
		t.Fatalf("AttachAgentSession: %v", err)
	}
	defer func() { _ = session.Close() }()
	_ = session.Stream(io.Discard, io.Discard)

	var decoded sandboxapi.AgentSessionRequest
	if err := json.Unmarshal(receivedBody, &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if decoded.Args == nil || len(*decoded.Args) != 2 {
		t.Errorf("args = %v", decoded.Args)
	}
	if decoded.Env == nil || (*decoded.Env)["KEY"] != "val" {
		t.Errorf("env = %v", decoded.Env)
	}
}

// ---------------------------------------------------------------------------
// Tests: error paths
// ---------------------------------------------------------------------------

func TestAttachAgentSession404(t *testing.T) {
	h := &upgradeHandler{
		t:            t,
		errorStatus:  http.StatusNotFound,
		errorMessage: "sandbox not found",
	}
	client, _ := newUpgradeTestClient(t, h)

	_, err := client.AttachAgentSession(t.Context(), "missing",
		sandboxapi.AgentSessionRequest{})
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !IsNotFound(err) {
		t.Errorf("expected NotFoundError, got: %v", err)
	}
}

func TestAttachAgentSession422(t *testing.T) {
	h := &upgradeHandler{
		t:            t,
		errorStatus:  http.StatusUnprocessableEntity,
		errorMessage: "start script missing",
	}
	client, _ := newUpgradeTestClient(t, h)

	_, err := client.AttachAgentSession(t.Context(), "sb",
		sandboxapi.AgentSessionRequest{})
	if err == nil {
		t.Fatal("expected error for 422")
	}
	if !strings.Contains(err.Error(), "start script") {
		t.Errorf("expected 'start script' in error, got: %v", err)
	}
}

func TestAttachAgentSession500(t *testing.T) {
	h := &upgradeHandler{
		t:            t,
		errorStatus:  http.StatusInternalServerError,
		errorMessage: "internal error",
	}
	client, _ := newUpgradeTestClient(t, h)

	_, err := client.AttachAgentSession(t.Context(), "sb",
		sandboxapi.AgentSessionRequest{})
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected '500' in error, got: %v", err)
	}
}

func TestAttachAgentSessionNoDialFunc(t *testing.T) {
	// A DaemonClient without dial set (shouldn't happen in practice, but guard it).
	c := &DaemonClient{} // no dial, no api
	_, err := c.AttachAgentSession(t.Context(), "sb", sandboxapi.AgentSessionRequest{})
	if err == nil {
		t.Fatal("expected error when dial is nil")
	}
}
