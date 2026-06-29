package orchestrator

import (
	"fmt"
	"io"
	"time"

	"github.com/glours/hakoniwa/internal/sandbox"
)

const (
	defaultPollInterval = 500 * time.Millisecond
	defaultPollTimeout  = 120 * time.Second
)

// Orchestrator coordinates sandbox lifecycle across the agents in a project.
// It holds the low-level clients and poll configuration used by Up, Down,
// Plan, and Ps.
type Orchestrator struct {
	Client       sandbox.Client
	Sbx          sandbox.SbxAdapter
	ProjectName  string
	Out          io.Writer
	PollInterval time.Duration
	PollTimeout  time.Duration

	// Driver and Stager are optional — set by the CLI wiring when session
	// driving is enabled (Task 2.6). When nil, Up skips session driving
	// (backward-compatible with infrastructure-only tests).
	Driver sandbox.SessionDriver
	Stager sandbox.FileStager
}

// NewOrchestrator creates an Orchestrator with sensible poll defaults.
// projectName must match project.Name from the config file; it is used as the
// sandbox-name prefix ("<project>-<agent>") and must be non-empty.
func NewOrchestrator(
	client sandbox.Client,
	sbx sandbox.SbxAdapter,
	projectName string,
	out io.Writer,
) (*Orchestrator, error) {
	if projectName == "" {
		return nil, fmt.Errorf("project name is required; set 'name:' in the project file")
	}
	if out == nil {
		out = io.Discard
	}
	return &Orchestrator{
		Client:       client,
		Sbx:          sbx,
		ProjectName:  projectName,
		Out:          out,
		PollInterval: defaultPollInterval,
		PollTimeout:  defaultPollTimeout,
	}, nil
}

// SandboxName returns the daemon sandbox name for the given agent key:
// "<projectName>-<agentKey>".
func (o *Orchestrator) SandboxName(agent string) string {
	return o.ProjectName + "-" + agent
}
