package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
)

// AgentAction describes what hako up would do for an agent.
type AgentAction string

const (
	ActionCreate   AgentAction = "create"   // sandbox does not exist
	ActionReuse    AgentAction = "reuse"    // sandbox exists; nothing to do
	ActionConverge AgentAction = "converge" // sandbox exists; ports/config differ
)

// PlanEntry contains everything the renderer needs to display one agent.
type PlanEntry struct {
	Agent         string
	Sandbox       string
	Action        AgentAction
	AgentKind     string
	Template      string
	CurrentStatus string // populated for reuse/converge

	// Declared configuration (for display)
	AllPorts   []string // all declared ports
	AddPorts   []string // ports to add (converge delta)
	SecretEnvs []string // env-var names only, never values
	Emits      []string
	DependsOn  map[string]config.DependsOnEntry
	Reach      []string
	Kits       []string
}

// Plan computes what hako up would do without making any changes.
// It validates the project, resolves agents, builds the graph, then compares
// desired state against live daemon state.
//
// Returns a slice of PlanEntry in topo order. Rendering is handled separately
// by RenderPlan so callers can format the output however they prefer.
func (o *Orchestrator) Plan(ctx context.Context, project *config.Project) ([]PlanEntry, error) {
	lr := &config.LoadResult{Project: project}
	if verr := config.Validate(lr); verr != nil {
		return nil, fmt.Errorf("validation: %w", verr)
	}

	agents := config.ResolveAgents(project)
	graph, err := BuildGraph(agents)
	if err != nil {
		return nil, fmt.Errorf("build dependency graph: %w", err)
	}

	entries := make([]PlanEntry, 0, len(agents))
	for _, agentName := range graph.Order() {
		ea := agents[agentName]
		sbxName := o.SandboxName(agentName)

		action := ActionCreate
		var addPorts []string
		var currentStatus string

		info, err := o.Client.InspectSandbox(ctx, sbxName)
		if err == nil {
			currentStatus = string(info.Status)
			existing, _ := o.Client.ListPublishedPorts(ctx, sbxName)
			var missing []string
			for _, spec := range ea.Ports {
				req, parseErr := sandbox.ParsePortSpec(spec)
				if parseErr != nil {
					return nil, fmt.Errorf("[%s] port spec %q: %w", agentName, spec, parseErr)
				}
				if !portAlreadyPublished(existing, req) {
					missing = append(missing, spec)
				}
			}
			if len(missing) > 0 || needsConverge(info, ea) {
				action = ActionConverge
				addPorts = missing
			} else {
				action = ActionReuse
			}
		} else if !sandbox.IsNotFound(err) {
			return nil, fmt.Errorf("[%s] inspect: %w", agentName, err)
		}

		// Collect secret env names (never values).
		var secretEnvs []string
		for _, s := range ea.Secrets {
			if s.Env != "" {
				secretEnvs = append(secretEnvs, s.Env)
			}
		}
		for _, s := range ea.Credentials {
			if s.Env != "" {
				secretEnvs = append(secretEnvs, s.Env)
			}
		}

		// Copy DependsOn map.
		depsCopy := make(map[string]config.DependsOnEntry, len(ea.DependsOn))
		for k, v := range ea.DependsOn {
			depsCopy[k] = v
		}

		entries = append(entries, PlanEntry{
			Agent:         agentName,
			Sandbox:       sbxName,
			Action:        action,
			AgentKind:     ea.AgentKind,
			Template:      ea.Template,
			CurrentStatus: currentStatus,
			AllPorts:      append([]string(nil), ea.Ports...),
			AddPorts:      addPorts,
			SecretEnvs:    secretEnvs,
			Emits:         append([]string(nil), ea.Emits...),
			DependsOn:     depsCopy,
			Reach:         append([]string(nil), ea.Reach...),
			Kits:          append([]string(nil), ea.Kits...),
		})
	}

	return entries, nil
}

// needsConverge returns true if the sandbox info differs from what the agent
// declares in terms of agent kind. Port differences are handled separately.
func needsConverge(info *sandbox.SandboxInfo, ea *config.EffectiveAgent) bool {
	if info.Agent != nil && *info.Agent != ea.AgentKind {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Ps
// ---------------------------------------------------------------------------

// PsEntry is one row in the ps table.
type PsEntry struct {
	Agent  string
	Name   string
	Status string
	Ports  []string // "host_port:sandbox_port/proto" formatted
}

// Ps lists the project's sandboxes filtered by the project-name prefix.
// It writes a table to o.Out and returns the entries.
func (o *Orchestrator) Ps(ctx context.Context) ([]PsEntry, error) {
	prefix := o.ProjectName + "-"

	all, err := o.Client.ListSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}

	var entries []PsEntry
	for _, info := range all {
		if !strings.HasPrefix(info.Name, prefix) {
			continue
		}
		agentName := strings.TrimPrefix(info.Name, prefix)

		ports, _ := o.Client.ListPublishedPorts(ctx, info.Name)
		portStrs := make([]string, 0, len(ports))
		for _, p := range ports {
			portStrs = append(portStrs, fmt.Sprintf("%d:%d/%s", p.HostPort, p.SandboxPort, p.Protocol))
		}

		entries = append(entries, PsEntry{
			Agent:  agentName,
			Name:   info.Name,
			Status: string(info.Status),
			Ports:  portStrs,
		})
	}

	// Sort by sandbox name for stable output.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Name < entries[j-1].Name; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}

	RenderPs(o.Out, o.ProjectName, entries)
	return entries, nil
}
