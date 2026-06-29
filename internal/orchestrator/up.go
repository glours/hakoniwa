package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// Up creates, configures, and starts all agents defined in the project file,
// walking them in topological dependency order.
//
// The operation is fully idempotent: re-running Up on an already-running
// project recreates nothing and skips any port bindings that already exist.
//
// Sequential topo-order processing naturally honours all depends_on gates
// (created and running): each agent is fully started before its dependents
// are processed, so both gate conditions are satisfied at the time dependents
// begin.
func (o *Orchestrator) Up(ctx context.Context, project *config.Project) error {
	agents := config.ResolveAgents(project)

	graph, err := BuildGraph(agents)
	if err != nil {
		return fmt.Errorf("build dependency graph: %w", err)
	}

	fmt.Fprintf(o.Out, "Ensuring daemon is running…\n")
	if err := o.Sbx.EnsureDaemon(ctx); err != nil {
		return fmt.Errorf("ensure daemon: %w", err)
	}

	for _, agentName := range graph.Order() {
		if err := o.ensureAgent(ctx, agentName, agents[agentName]); err != nil {
			return err
		}
	}

	fmt.Fprintf(o.Out, "All agents running.\n")
	return nil
}

// ensureAgent find-or-creates, publishes ports, starts, and waits for a
// single agent sandbox to reach the running state.
func (o *Orchestrator) ensureAgent(ctx context.Context, agentName string, ea *config.EffectiveAgent) error {
	sbxName := o.SandboxName(agentName)
	fmt.Fprintf(o.Out, "[%s] ensuring sandbox %s…\n", agentName, sbxName)

	// 1. Find or create — inspect first; only call sbx create when absent.
	_, inspErr := o.Client.InspectSandbox(ctx, sbxName)
	if inspErr != nil {
		if !sandbox.IsNotFound(inspErr) {
			return fmt.Errorf("[%s] inspect: %w", agentName, inspErr)
		}
		fmt.Fprintf(o.Out, "[%s] creating sandbox %s\n", agentName, sbxName)
		if err := o.Sbx.Create(ctx, sandbox.CreateRequest{
			Name:     sbxName,
			Agent:    ea.AgentKind,
			Template: ea.Template,
			CPUs:     ea.Resources.CPUs,
			MemoryMB: ea.Resources.Memory,
			Kits:     append([]string(nil), ea.Kits...),
		}); err != nil {
			return fmt.Errorf("[%s] create: %w", agentName, err)
		}
	} else {
		fmt.Fprintf(o.Out, "[%s] sandbox %s already exists, reusing\n", agentName, sbxName)
	}

	// 2. Publish declared ports (idempotent diff against existing bindings).
	if len(ea.Ports) > 0 {
		if err := o.publishPorts(ctx, agentName, sbxName, ea.Ports); err != nil {
			return err
		}
	}

	// 3. Start the sandbox (the daemon is idempotent on already-running).
	fmt.Fprintf(o.Out, "[%s] starting sandbox %s\n", agentName, sbxName)
	if _, err := o.Client.StartSandbox(ctx, sbxName); err != nil {
		return fmt.Errorf("[%s] start: %w", agentName, err)
	}

	// 4. Poll until status == running.
	fmt.Fprintf(o.Out, "[%s] waiting for running…\n", agentName)
	if err := o.waitRunning(ctx, agentName, sbxName); err != nil {
		return err
	}

	fmt.Fprintf(o.Out, "[%s] running ✓\n", agentName)
	return nil
}

// publishPorts computes the diff between declared and existing port bindings
// and calls PublishPorts only for the missing ones.
func (o *Orchestrator) publishPorts(ctx context.Context, agentName, sbxName string, portSpecs []string) error {
	existing, err := o.Client.ListPublishedPorts(ctx, sbxName)
	if err != nil && !sandbox.IsNotFound(err) {
		return fmt.Errorf("[%s] list ports: %w", agentName, err)
	}

	var toPublish []sandbox.PortPublishRequest
	for _, spec := range portSpecs {
		req, err := sandbox.ParsePortSpec(spec)
		if err != nil {
			return fmt.Errorf("[%s] port spec %q: %w", agentName, spec, err)
		}
		if !portAlreadyPublished(existing, req) {
			toPublish = append(toPublish, req)
		}
	}

	if len(toPublish) == 0 {
		fmt.Fprintf(o.Out, "[%s] all ports already published, skipping\n", agentName)
		return nil
	}

	fmt.Fprintf(o.Out, "[%s] publishing %d port(s)\n", agentName, len(toPublish))
	if _, err := o.Client.PublishPorts(ctx, sbxName, toPublish); err != nil {
		return fmt.Errorf("[%s] publish ports: %w", agentName, err)
	}
	return nil
}

// portAlreadyPublished returns true if req is already represented in existing.
// Protocol defaults to "tcp" when omitted from the request.
func portAlreadyPublished(existing []sandbox.PublishedPort, req sandbox.PortPublishRequest) bool {
	reqProto := string(sandboxapi.PortPublishRequestProtocolTcp)
	if req.Protocol != nil {
		reqProto = string(*req.Protocol)
	}
	for _, p := range existing {
		if p.SandboxPort != req.SandboxPort {
			continue
		}
		if string(p.Protocol) != reqProto {
			continue
		}
		// If a specific host port was requested, verify it matches.
		// A zero host port means auto-assign; any existing binding matches.
		if req.HostPort != 0 && p.HostPort != req.HostPort {
			continue
		}
		return true
	}
	return false
}

// waitRunning polls InspectSandbox until the sandbox status transitions to
// "running" or the PollTimeout elapses.
func (o *Orchestrator) waitRunning(ctx context.Context, agentName, sbxName string) error {
	deadline := time.Now().Add(o.PollTimeout)
	for {
		info, err := o.Client.InspectSandbox(ctx, sbxName)
		if err != nil {
			return fmt.Errorf("[%s] poll inspect: %w", agentName, err)
		}
		if info.Status == sandboxapi.SandboxInfoStatusRunning {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("[%s] timed out after %s waiting for %s to be running (last status: %s)",
				agentName, o.PollTimeout, sbxName, info.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.PollInterval):
		}
	}
}
