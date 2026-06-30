package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// defaultLinuxSocketPath is the sandboxd socket path on Linux.
const defaultLinuxSocketPath = "/run/sandboxd/sandboxd.sock"

// resolveSocketPath returns the sandboxd Unix socket path to use.
//
// Resolution order (mirrors docker/sandboxes sandboxlib.SocketPath):
//  1. DOCKER_SANDBOXES_API env var — used as-is when set.
//  2. macOS: $HOME/Library/Application Support/com.docker.sandboxes/sandboxes/sandboxd/sandboxd.sock
//  3. Linux fallback: /run/sandboxd/sandboxd.sock
//
// The macOS path is derived from the storagekit platform ID
// (com.docker.sandboxes) and default app name (sandboxes) used by
// docker/sandboxes (sandboxlib/storagepaths package).
func resolveSocketPath() string {
	if v := os.Getenv("DOCKER_SANDBOXES_API"); v != "" {
		return v
	}
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(
				home,
				"Library", "Application Support",
				"com.docker.sandboxes", "sandboxes", "sandboxd", "sandboxd.sock",
			)
		}
	}
	return defaultLinuxSocketPath
}

// NotFoundError is returned by Client methods when the daemon returns HTTP 404.
type NotFoundError struct {
	Resource string // e.g. "sandbox bugfix-session-reproducer"
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s: not found", e.Resource)
}

// IsNotFound returns true if err (or any wrapped error) is a *NotFoundError.
func IsNotFound(err error) bool {
	return errors.As(err, new(*NotFoundError))
}

// SandboxInfo is an alias for the generated type, re-exported for callers
// that should not need to import the sandboxapi sub-package directly.
type SandboxInfo = sandboxapi.SandboxInfo

// PublishedPort is an alias for the generated type.
type PublishedPort = sandboxapi.PublishedPort

// PortPublishRequest is an alias for the generated type.
type PortPublishRequest = sandboxapi.PortPublishRequest

// PortKey is an alias for the generated type.
type PortKey = sandboxapi.PortKey

// Client defines the operations Hakoniwa's orchestrator uses against sandboxd.
// The interface is defined here (not in sandboxapi) so callers in other
// packages can mock it without importing the generated code.
type Client interface {
	// ListSandboxes returns all sandboxes known to the daemon.
	ListSandboxes(ctx context.Context) ([]SandboxInfo, error)

	// InspectSandbox returns the state of the named sandbox.
	// Returns *NotFoundError if the sandbox does not exist.
	InspectSandbox(ctx context.Context, name string) (*SandboxInfo, error)

	// StartSandbox starts a stopped sandbox. Idempotent if already running.
	StartSandbox(ctx context.Context, name string) (*SandboxInfo, error)

	// StopSandbox stops a running sandbox.
	StopSandbox(ctx context.Context, name string) (*SandboxInfo, error)

	// DeleteSandbox stops and removes a sandbox (including its network/state).
	// Returns *NotFoundError if the sandbox does not exist.
	DeleteSandbox(ctx context.Context, name string) error

	// ListPublishedPorts returns the currently published ports for a sandbox.
	ListPublishedPorts(ctx context.Context, name string) ([]PublishedPort, error)

	// PublishPorts publishes the given port mappings for a sandbox.
	// Returns the list of resulting port bindings, including auto-assigned
	// host ports for entries where host_port was 0.
	PublishPorts(ctx context.Context, name string, ports []PortPublishRequest) ([]PublishedPort, error)

	// UnpublishPorts removes the given port mappings from a sandbox.
	UnpublishPorts(ctx context.Context, name string, keys []PortKey) error
}

// DaemonClient is the live implementation of Client that talks to sandboxd
// over the daemon Unix socket.
//
// dial and baseURL are used by the HTTP-upgrade helpers (AttachAgentSession)
// which need a raw net.Conn — the standard http.Client cannot handle
// protocol upgrades.
type DaemonClient struct {
	api     *sandboxapi.ClientWithResponses
	dial    func(ctx context.Context) (net.Conn, error)
	baseURL string // scheme://host (no path) — used to build upgrade request URLs
}

// NewDaemonClient creates a DaemonClient that dials the sandboxd Unix socket.
// The socket path is resolved via resolveSocketPath (DOCKER_SANDBOXES_API,
// then macOS platform path, then Linux fallback).
func NewDaemonClient() (*DaemonClient, error) {
	socketPath := resolveSocketPath()

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	dialFn := func(ctx context.Context) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socketPath)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialFn(ctx)
		},
	}
	httpClient := &http.Client{Transport: transport, Timeout: 30 * time.Second}

	// The generated client uses "http://daemon" as a dummy base URL;
	// all requests go through the custom transport above.
	api, err := sandboxapi.NewClientWithResponses("http://daemon",
		sandboxapi.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("create sandboxd client: %w", err)
	}
	return &DaemonClient{api: api, dial: dialFn, baseURL: "http://daemon"}, nil
}

// NewDaemonClientForURL creates a DaemonClient that uses the given base URL
// instead of the Unix socket. Intended for unit tests with net/http/httptest.
func NewDaemonClientForURL(baseURL string) (*DaemonClient, error) {
	api, err := sandboxapi.NewClientWithResponses(baseURL)
	if err != nil {
		return nil, fmt.Errorf("create sandboxd client: %w", err)
	}

	// Parse the URL to extract host for raw TCP dialing (upgrade helper).
	u, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	host := u.Host
	network := "tcp"
	if u.Scheme == "unix" {
		network = "unix"
		host = u.Path
	}
	dialFn := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, host)
	}
	return &DaemonClient{api: api, dial: dialFn, baseURL: baseURL}, nil
}

// parseBaseURL parses the base URL, returning the url.URL.
func parseBaseURL(rawURL string) (*neturl.URL, error) {
	return neturl.Parse(rawURL)
}

// --- Client interface implementation ---

func (c *DaemonClient) ListSandboxes(ctx context.Context) ([]SandboxInfo, error) {
	resp, err := c.api.ListSandboxesWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("listSandboxes: %w", err)
	}
	if resp.HTTPResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listSandboxes: unexpected status %d", resp.HTTPResponse.StatusCode)
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	return *resp.JSON200, nil
}

func (c *DaemonClient) InspectSandbox(ctx context.Context, name string) (*SandboxInfo, error) {
	resp, err := c.api.InspectSandboxWithResponse(ctx, sandboxapi.SandboxName(name))
	if err != nil {
		return nil, fmt.Errorf("inspectSandbox %q: %w", name, err)
	}
	if resp.HTTPResponse.StatusCode == http.StatusNotFound {
		return nil, &NotFoundError{Resource: "sandbox " + name}
	}
	if resp.HTTPResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("inspectSandbox %q: unexpected status %d", name, resp.HTTPResponse.StatusCode)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("inspectSandbox %q: empty response body", name)
	}
	return resp.JSON200, nil
}

func (c *DaemonClient) StartSandbox(ctx context.Context, name string) (*SandboxInfo, error) {
	resp, err := c.api.StartSandboxWithResponse(ctx, sandboxapi.SandboxName(name))
	if err != nil {
		return nil, fmt.Errorf("startSandbox %q: %w", name, err)
	}
	if resp.HTTPResponse.StatusCode == http.StatusNotFound {
		return nil, &NotFoundError{Resource: "sandbox " + name}
	}
	if resp.HTTPResponse.StatusCode == http.StatusConflict {
		// 409 is returned for port-replay-conflict.
		if resp.JSON409 != nil && resp.JSON409.Message != "" {
			return nil, fmt.Errorf("startSandbox %q: conflict: %s", name, resp.JSON409.Message)
		}
		return nil, fmt.Errorf("startSandbox %q: port replay conflict (a host port is already in use)", name)
	}
	if resp.HTTPResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("startSandbox %q: unexpected status %d", name, resp.HTTPResponse.StatusCode)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("startSandbox %q: empty response body", name)
	}
	return resp.JSON200, nil
}

func (c *DaemonClient) StopSandbox(ctx context.Context, name string) (*SandboxInfo, error) {
	resp, err := c.api.StopSandboxWithResponse(ctx, sandboxapi.SandboxName(name))
	if err != nil {
		return nil, fmt.Errorf("stopSandbox %q: %w", name, err)
	}
	if resp.HTTPResponse.StatusCode == http.StatusNotFound {
		return nil, &NotFoundError{Resource: "sandbox " + name}
	}
	if resp.HTTPResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stopSandbox %q: unexpected status %d", name, resp.HTTPResponse.StatusCode)
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("stopSandbox %q: empty response body", name)
	}
	return resp.JSON200, nil
}

func (c *DaemonClient) DeleteSandbox(ctx context.Context, name string) error {
	resp, err := c.api.DeleteSandboxWithResponse(ctx, sandboxapi.SandboxName(name))
	if err != nil {
		return fmt.Errorf("deleteSandbox %q: %w", name, err)
	}
	if resp.HTTPResponse.StatusCode == http.StatusNotFound {
		return &NotFoundError{Resource: "sandbox " + name}
	}
	if resp.HTTPResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("deleteSandbox %q: unexpected status %d", name, resp.HTTPResponse.StatusCode)
	}
	return nil
}

func (c *DaemonClient) ListPublishedPorts(ctx context.Context, name string) ([]PublishedPort, error) {
	resp, err := c.api.ListPublishedPortsWithResponse(ctx, sandboxapi.SandboxName(name))
	if err != nil {
		return nil, fmt.Errorf("listPublishedPorts %q: %w", name, err)
	}
	if resp.HTTPResponse.StatusCode == http.StatusNotFound {
		return nil, &NotFoundError{Resource: "sandbox " + name}
	}
	if resp.HTTPResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listPublishedPorts %q: unexpected status %d", name, resp.HTTPResponse.StatusCode)
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	return *resp.JSON200, nil
}

func (c *DaemonClient) PublishPorts(ctx context.Context, name string, ports []PortPublishRequest) ([]PublishedPort, error) {
	resp, err := c.api.PublishPortsWithResponse(ctx, sandboxapi.SandboxName(name),
		sandboxapi.PublishPortsJSONRequestBody(ports))
	if err != nil {
		return nil, fmt.Errorf("publishPorts %q: %w", name, err)
	}
	if resp.HTTPResponse.StatusCode == http.StatusNotFound {
		return nil, &NotFoundError{Resource: "sandbox " + name}
	}
	if resp.HTTPResponse.StatusCode == http.StatusBadRequest {
		// 400 carries a structured ErrorResponse (e.g. port conflict).
		if resp.JSON400 != nil && resp.JSON400.Message != "" {
			return nil, fmt.Errorf("publishPorts %q: %s", name, resp.JSON400.Message)
		}
		return nil, fmt.Errorf("publishPorts %q: bad request (port conflict?)", name)
	}
	if resp.HTTPResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("publishPorts %q: unexpected status %d", name, resp.HTTPResponse.StatusCode)
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	return *resp.JSON200, nil
}

func (c *DaemonClient) UnpublishPorts(ctx context.Context, name string, keys []PortKey) error {
	resp, err := c.api.UnpublishPortsWithResponse(ctx, sandboxapi.SandboxName(name),
		sandboxapi.UnpublishPortsJSONRequestBody(keys))
	if err != nil {
		return fmt.Errorf("unpublishPorts %q: %w", name, err)
	}
	if resp.HTTPResponse.StatusCode == http.StatusNotFound {
		return &NotFoundError{Resource: "sandbox " + name}
	}
	if resp.HTTPResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("unpublishPorts %q: unexpected status %d", name, resp.HTTPResponse.StatusCode)
	}
	return nil
}
