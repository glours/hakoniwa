package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// SbxAdapter is the interface for the `sbx` CLI cold path.
// It handles operations that require credential/kit resolution which only
// the `sbx` binary can do today (see design §1 hybrid rationale).
type SbxAdapter interface {
	// EnsureDaemon starts the daemon if it is not already running.
	EnsureDaemon(ctx context.Context) error

	// Create creates a new sandbox with the given parameters.
	// Returns the sandbox name as seen by the daemon (same as the input name).
	// If a sandbox with the given name already exists (409-equivalent), it is
	// reused — no error is returned.
	Create(ctx context.Context, req CreateRequest) error

	// SecretSetCustom injects a resolved secret into a sandbox.
	SecretSetCustom(ctx context.Context, sandboxName string, s SecretSetRequest) error

	// PolicySetDefault sets the host-global network policy preset.
	// Idempotent: no error if already set to the same value.
	PolicySetDefault(ctx context.Context, preset string) error

	// PolicyAllow adds an allow rule to a sandbox's network policy.
	PolicyAllow(ctx context.Context, sandboxName, rule string) error

	// PolicyDeny adds a deny rule to a sandbox's network policy.
	PolicyDeny(ctx context.Context, sandboxName, rule string) error

	// PolicyRemove removes a named rule from a sandbox's network policy.
	PolicyRemove(ctx context.Context, sandboxName, ruleID string) error

	// List returns the current list of sandboxes as a JSON-decoded slice.
	List(ctx context.Context) ([]SbxListEntry, error)
}

// CreateRequest holds the parameters for sbx create.
type CreateRequest struct {
	Name     string
	Agent    string   // agent kind (e.g. "claude", "codex")
	Template string   // optional container image override
	CPUs     float64  // 0 = use daemon default
	MemoryMB int      // 0 = use daemon default
	Kits     []string // kit refs (oci://, dir, .zip)
}

// SecretSetRequest holds the parameters for sbx secret set-custom.
type SecretSetRequest struct {
	Value       string // resolved secret value (stdout of the value command)
	Env         string // env var name to bind inside the sandbox
	Host        string // optional: restrict to this host
	Placeholder string // optional: replace literal placeholder in prompts
}

// SbxListEntry is the minimal subset of sbx ls --json we care about.
type SbxListEntry struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Status string `json:"status"`
}

// SbxCLIAdapter is the live implementation of SbxAdapter that shells out to `sbx`.
type SbxCLIAdapter struct {
	// sbxPath is the full path to the sbx binary.
	// Defaults to "sbx" (PATH-resolved).
	sbxPath string

	// daemonClient is used after sbx create to re-inspect the sandbox (since
	// sbx create has no --json output).
	daemonClient Client
}

// NewSbxCLIAdapter creates an SbxCLIAdapter with `sbx` resolved on PATH.
func NewSbxCLIAdapter(daemonClient Client) *SbxCLIAdapter {
	return &SbxCLIAdapter{sbxPath: "sbx", daemonClient: daemonClient}
}

// NewSbxCLIAdapterForTest creates an SbxCLIAdapter with the given binary path.
// Exported for use in package-external tests.
func NewSbxCLIAdapterForTest(sbxPath string, daemonClient Client) *SbxCLIAdapter {
	return &SbxCLIAdapter{sbxPath: sbxPath, daemonClient: daemonClient}
}

// run executes a sbx subcommand and returns stdout. Stderr is captured and
// included in the error message on non-zero exit.
func (a *SbxCLIAdapter) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, a.sbxPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("sbx %s: %w\nstderr: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (a *SbxCLIAdapter) EnsureDaemon(ctx context.Context) error {
	_, err := a.run(ctx, "daemon", "status")
	if err == nil {
		return nil
	}
	// Daemon not running — attempt to start it.
	if _, startErr := a.run(ctx, "daemon", "start"); startErr != nil {
		return fmt.Errorf("daemon not running and start failed: %w", startErr)
	}
	return nil
}

func (a *SbxCLIAdapter) Create(ctx context.Context, req CreateRequest) error {
	args := []string{"create", "--name", req.Name}
	if req.Template != "" {
		args = append(args, "--template", req.Template)
	}
	if req.CPUs > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%g", req.CPUs))
	}
	if req.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", req.MemoryMB))
	}
	for _, kit := range req.Kits {
		args = append(args, "--kit", kit)
	}
	// The positional arg is the agent kind; "." is the workspace (current dir).
	args = append(args, req.Agent, ".")

	_, err := a.run(ctx, args...)
	if err != nil {
		// sbx create returns non-zero if the sandbox already exists.
		// Treat it as a reuse (idempotent): re-inspect to confirm it exists
		// and return nil. If the daemon client can inspect it, we're good.
		if _, inspErr := a.daemonClient.InspectSandbox(ctx, req.Name); inspErr == nil {
			// Sandbox exists — the create "error" was a conflict; reuse.
			return nil
		}
		// Sandbox does not exist and create failed — return original error.
		return err
	}
	return nil
}

func (a *SbxCLIAdapter) SecretSetCustom(ctx context.Context, sandboxName string, s SecretSetRequest) error {
	args := []string{"secret", "set-custom", sandboxName}
	if s.Placeholder != "" {
		args = append(args, "--placeholder", s.Placeholder)
	}
	if s.Host != "" {
		args = append(args, "--host", s.Host)
	}
	if s.Env != "" {
		args = append(args, "--env", s.Env)
	}
	args = append(args, "--value", s.Value)
	_, err := a.run(ctx, args...)
	return err
}

func (a *SbxCLIAdapter) PolicySetDefault(ctx context.Context, preset string) error {
	_, err := a.run(ctx, "policy", "set-default", preset)
	return err
}

func (a *SbxCLIAdapter) PolicyAllow(ctx context.Context, sandboxName, rule string) error {
	_, err := a.run(ctx, "policy", "allow", "--sandbox", sandboxName, rule)
	return err
}

func (a *SbxCLIAdapter) PolicyDeny(ctx context.Context, sandboxName, rule string) error {
	_, err := a.run(ctx, "policy", "deny", "--sandbox", sandboxName, rule)
	return err
}

func (a *SbxCLIAdapter) PolicyRemove(ctx context.Context, sandboxName, ruleID string) error {
	_, err := a.run(ctx, "policy", "rm", "--sandbox", sandboxName, ruleID)
	return err
}

func (a *SbxCLIAdapter) List(ctx context.Context) ([]SbxListEntry, error) {
	out, err := a.run(ctx, "ls", "--json")
	if err != nil {
		return nil, err
	}
	var entries []SbxListEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("sbx ls --json: unmarshal: %w", err)
	}
	return entries, nil
}
