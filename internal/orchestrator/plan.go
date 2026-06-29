package orchestrator

import (
	"context"
	"fmt"
	"io"
	"sort"
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

// PlanEntry is one line in the plan output.
type PlanEntry struct {
	Agent   string
	Sandbox string
	Action  AgentAction
	Ports   []string // port specs that would be published
}

// Plan computes what hako up would do without making any changes.
// It validates the project, resolves agents, builds the graph, then compares
// desired state against live daemon state.
//
// Returns a slice of PlanEntry in topo order and writes a human-readable
// summary to o.Out.
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
		var portsToDeclare []string

		info, err := o.Client.InspectSandbox(ctx, sbxName)
		if err == nil {
			// Sandbox exists — check if ports differ.
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
				portsToDeclare = missing
			} else {
				action = ActionReuse
			}
		} else if !sandbox.IsNotFound(err) {
			return nil, fmt.Errorf("[%s] inspect: %w", agentName, err)
		} else {
			// Not found — would create.
			portsToDeclare = append([]string(nil), ea.Ports...)
		}

		entry := PlanEntry{
			Agent:   agentName,
			Sandbox: sbxName,
			Action:  action,
			Ports:   portsToDeclare,
		}
		entries = append(entries, entry)

		verb := string(action)
		portStr := ""
		if len(portsToDeclare) > 0 {
			portStr = " ports=[" + strings.Join(portsToDeclare, ", ") + "]"
		}
		fmt.Fprintf(o.Out, "  %s\t%s\t%s%s\n", agentName, sbxName, verb, portStr)
	}

	return entries, nil
}

// needsConverge returns true if the sandbox info differs from what the agent
// declares in terms of agent kind or template. Port differences are handled
// separately in Plan.
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
		sort.Strings(portStrs)

		entries = append(entries, PsEntry{
			Agent:  agentName,
			Name:   info.Name,
			Status: string(info.Status),
			Ports:  portStrs,
		})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	// Print table.
	fmt.Fprintf(o.Out, "%-20s  %-35s  %-10s  %s\n", "AGENT", "SANDBOX", "STATUS", "PORTS")
	fmt.Fprintf(o.Out, "%s\n", strings.Repeat("-", 80))
	for _, e := range entries {
		ports := strings.Join(e.Ports, ", ")
		if ports == "" {
			ports = "-"
		}
		fmt.Fprintf(o.Out, "%-20s  %-35s  %-10s  %s\n", e.Agent, e.Name, e.Status, ports)
	}

	return entries, nil
}

// WritePlanSummary writes a summary header before the per-agent plan lines.
func WritePlanSummary(out io.Writer, project *config.Project) {
	fmt.Fprintf(out, "Plan for project %q:\n", project.Name)
}
