package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// defaultSocketPath is the fallback Unix socket path for sandboxd.
// Production code reads DOCKER_SANDBOXES_API first.
const defaultSocketPath = "/run/sandboxd/sandboxd.sock"

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
	PublishPorts(ctx context.Context, name string, ports []PortPublishRequest) error

	// UnpublishPorts removes the given port mappings from a sandbox.
	UnpublishPorts(ctx context.Context, name string, keys []PortKey) error
}

// DaemonClient is the live implementation of Client that talks to sandboxd
// over the daemon Unix socket.
type DaemonClient struct {
	api *sandboxapi.ClientWithResponses
}

// NewDaemonClient creates a DaemonClient that dials the sandboxd Unix socket.
// The socket path is read from $DOCKER_SANDBOXES_API, falling back to
// defaultSocketPath.
func NewDaemonClient() (*DaemonClient, error) {
	socketPath := os.Getenv("DOCKER_SANDBOXES_API")
	if socketPath == "" {
		socketPath = defaultSocketPath
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socketPath)
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
	return &DaemonClient{api: api}, nil
}

// NewDaemonClientForURL creates a DaemonClient that uses the given base URL
// instead of the Unix socket. Intended for unit tests with net/http/httptest.
func NewDaemonClientForURL(baseURL string) (*DaemonClient, error) {
	api, err := sandboxapi.NewClientWithResponses(baseURL)
	if err != nil {
		return nil, fmt.Errorf("create sandboxd client: %w", err)
	}
	return &DaemonClient{api: api}, nil
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

func (c *DaemonClient) PublishPorts(ctx context.Context, name string, ports []PortPublishRequest) error {
	resp, err := c.api.PublishPortsWithResponse(ctx, sandboxapi.SandboxName(name),
		sandboxapi.PublishPortsJSONRequestBody(ports))
	if err != nil {
		return fmt.Errorf("publishPorts %q: %w", name, err)
	}
	if resp.HTTPResponse.StatusCode == http.StatusNotFound {
		return &NotFoundError{Resource: "sandbox " + name}
	}
	if resp.HTTPResponse.StatusCode == http.StatusBadRequest {
		// 400 carries a structured ErrorResponse (e.g. port conflict).
		if resp.JSON400 != nil && resp.JSON400.Message != "" {
			return fmt.Errorf("publishPorts %q: %s", name, resp.JSON400.Message)
		}
		return fmt.Errorf("publishPorts %q: bad request (port conflict?)", name)
	}
	if resp.HTTPResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("publishPorts %q: unexpected status %d", name, resp.HTTPResponse.StatusCode)
	}
	return nil
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
