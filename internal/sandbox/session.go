package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// execIDHeader is the response header name carrying the exec ID after upgrade.
const execIDHeader = "Sandboxes-Exec-Id"

// agentSessionPath is the URL path template for the agent/session endpoint.
const agentSessionPath = "/sandbox/%s/agent/session"

// execInspectPath is the URL path template for exec inspect (exit code).
const execInspectPollInterval = 200 * time.Millisecond

// Session is the interface for a live agent-session stream. It is returned by
// SessionDriver.AttachAgentSession and implemented by *AgentSession. Defining
// it as an interface lets other packages (orchestrator, tests) mock it.
type Session interface {
	// ExecID returns the Docker-backend exec ID from the 101 response header.
	// Used for resize and post-stream exit-code inspection.
	ExecID() string

	// Stream copies the agent's output to stdout and stderr until the session
	// ends (server closes the connection). Non-TTY stdcopy framing is assumed.
	// Returns nil on clean EOF.
	Stream(stdout, stderr io.Writer) error

	// ExitCode inspects the exec via the daemon API and returns the exit code.
	// Call this after Stream returns to obtain the final exit status.
	// Polls until running=false or ctx is cancelled.
	ExitCode(ctx context.Context) (int, error)

	// Close closes the underlying connection.
	Close() error
}

// SessionDriver is implemented by *DaemonClient. It is separated from the
// Client interface so callers that only need request/response ops (and their
// fakes) are not forced to implement the streaming method.
type SessionDriver interface {
	AttachAgentSession(ctx context.Context, name string, req sandboxapi.AgentSessionRequest) (Session, error)
}

// Verify at compile time.
var _ SessionDriver = (*DaemonClient)(nil)

// AgentSession is the live implementation of Session returned by
// DaemonClient.AttachAgentSession. It wraps the upgraded connection and
// provides stream demultiplexing and exec-state inspection.
type AgentSession struct {
	conn        net.Conn
	reader      *bufio.Reader // wraps conn; may hold buffered bytes from the handshake
	execID      string
	api         *sandboxapi.ClientWithResponses // for InspectExec after stream ends
	sandboxName string
}

// Verify at compile time.
var _ Session = (*AgentSession)(nil)

// ExecID returns the exec ID received in the 101 response.
func (s *AgentSession) ExecID() string { return s.execID }

// Stream reads stdcopy-framed output from the upgraded connection, routing
// each frame to stdout or stderr. Blocks until the server closes the stream
// (clean EOF). Returns nil on clean termination.
func (s *AgentSession) Stream(stdout, stderr io.Writer) error {
	return demuxStdcopy(s.reader, stdout, stderr)
}

// ExitCode polls GET /sandbox/{name}/exec/{execID} until the exec is no longer
// running, then returns the exit code. Polling interval is execInspectPollInterval.
func (s *AgentSession) ExitCode(ctx context.Context) (int, error) {
	if s.execID == "" {
		return 0, fmt.Errorf("exec ID not available — cannot inspect exit code")
	}
	for {
		resp, err := s.api.InspectExecWithResponse(ctx, sandboxapi.SandboxName(s.sandboxName), s.execID)
		if err != nil {
			return 0, fmt.Errorf("inspectExec %q: %w", s.execID, err)
		}
		if resp.HTTPResponse.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("inspectExec %q: unexpected status %d", s.execID, resp.HTTPResponse.StatusCode)
		}
		if resp.JSON200 == nil {
			return 0, fmt.Errorf("inspectExec %q: empty response", s.execID)
		}
		if !resp.JSON200.Running {
			return resp.JSON200.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(execInspectPollInterval):
		}
	}
}

// Close closes the underlying connection.
func (s *AgentSession) Close() error { return s.conn.Close() }

// ---------------------------------------------------------------------------
// AttachAgentSession — hand-written HTTP-upgrade helper
// ---------------------------------------------------------------------------

// AttachAgentSession opens an agent session over an HTTP-upgrade connection.
//
// It dials a raw connection (unix socket or TCP, depending on how the
// DaemonClient was constructed), sends a POST /sandbox/{name}/agent/session
// with Connection: Upgrade / Upgrade: tcp headers, and expects a 101
// Switching Protocols response. On success it returns an *AgentSession whose
// reader wraps the upgraded connection.
//
// Error mapping:
//   - HTTP 404  → *NotFoundError
//   - HTTP 422  → error with message (start script missing; caller should
//     retry with start_command)
//   - other non-101 → error including the ErrorResponse message from the body
func (c *DaemonClient) AttachAgentSession(
	ctx context.Context,
	name string,
	req sandboxapi.AgentSessionRequest,
) (Session, error) {
	if c.dial == nil {
		return nil, fmt.Errorf("AttachAgentSession: DaemonClient has no dial function (use NewDaemonClient or NewDaemonClientForURL)")
	}

	// Serialise the request body.
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("AttachAgentSession: marshal request: %w", err)
	}

	// Dial a raw connection.
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("AttachAgentSession: dial: %w", err)
	}

	// Build the path from the baseURL.
	path := fmt.Sprintf(agentSessionPath, name)
	u, err := buildURL(c.baseURL, path)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("AttachAgentSession: build URL: %w", err)
	}

	// Construct the HTTP request with upgrade headers.
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(bodyBytes))
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("AttachAgentSession: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Connection", "Upgrade")
	httpReq.Header.Set("Upgrade", "tcp")

	// Write the request to the raw connection.
	if err := httpReq.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("AttachAgentSession: write request: %w", err)
	}

	// Read the response. The bufio.Reader may buffer bytes from the upgraded
	// stream, so we keep it and use it as the session reader.
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, httpReq)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("AttachAgentSession: read response: %w", err)
	}
	// Consume and close the response body (it is empty for non-upgrade status
	// codes and nil/empty for 101). This does NOT consume stream bytes in br.
	if resp.Body != nil {
		if resp.StatusCode != http.StatusSwitchingProtocols {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			_ = conn.Close()
			return nil, mapUpgradeError(resp.StatusCode, name, body)
		}
		_ = resp.Body.Close()
	} else if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, mapUpgradeError(resp.StatusCode, name, nil)
	}

	execID := resp.Header.Get(execIDHeader)

	return &AgentSession{
		conn:        conn,
		reader:      br,
		execID:      execID,
		api:         c.api,
		sandboxName: name,
	}, nil
}

// mapUpgradeError converts a non-101 HTTP response into a typed error.
func mapUpgradeError(statusCode int, name string, body []byte) error {
	msg := extractErrorMessage(body)
	switch statusCode {
	case http.StatusNotFound:
		return &NotFoundError{Resource: "sandbox " + name}
	case http.StatusUnprocessableEntity:
		if msg != "" {
			return fmt.Errorf("AttachAgentSession %q: start script missing (422): %s — retry with start_command", name, msg)
		}
		return fmt.Errorf("AttachAgentSession %q: start script missing (422) — retry with start_command", name)
	default:
		if msg != "" {
			return fmt.Errorf("AttachAgentSession %q: unexpected status %d: %s", name, statusCode, msg)
		}
		return fmt.Errorf("AttachAgentSession %q: unexpected status %d", name, statusCode)
	}
}

// extractErrorMessage tries to decode an ErrorResponse JSON body and return
// the message field. Returns "" on any failure.
func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var e sandboxapi.ErrorResponse
	if err := json.Unmarshal(body, &e); err != nil {
		return ""
	}
	return e.Message
}

// buildURL builds a full URL from a base (scheme://host) and a path.
func buildURL(base, path string) (string, error) {
	if base == "" {
		return "", fmt.Errorf("empty base URL")
	}
	// Trim any trailing slash from base, ensure path starts with /
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	if len(path) == 0 || path[0] != '/' {
		path = "/" + path
	}
	return base + path, nil
}
