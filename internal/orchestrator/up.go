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
// When Driver and Stager are set on the Orchestrator, Up also drives each
// agent's session after starting its sandbox, fires emitted channels, and
// gates subscribers until their on_event channels have fired (fan-out/fan-in).
//
// The operation is fully idempotent: re-running Up on an already-running
// project recreates nothing and skips any port bindings that already exist.
func (o *Orchestrator) Up(ctx context.Context, project *config.Project) error {
	agents := config.ResolveAgents(project)

	graph, err := BuildGraph(agents)
	if err != nil {
		return fmt.Errorf("build dependency graph: %w", err)
	}

	logf(o.Out, "Ensuring daemon is running\u2026\n")
	if err := o.Sbx.EnsureDaemon(ctx); err != nil {
		return fmt.Errorf("ensure daemon: %w", err)
	}

	// Build the channel registry from the project's declared channels.
	// emitterOf maps channel name -> agent key (from each agent's Emits list).
	emitterOf := make(map[string]string, len(project.Channels))
	for _, name := range graph.Order() {
		for _, ch := range agents[name].Emits {
			emitterOf[ch] = name
		}
	}
	reg := NewChannelRegistry(project.Channels, emitterOf)

	// Build a GateWaiter when session driving is enabled.
	var gw *GateWaiter
	if o.Driver != nil {
		gw = &GateWaiter{
			Registry: reg,
			Stager:   o.Stager,
			Out:      o.Out,
		}
	}

	for _, agentName := range graph.Order() {
		ea := agents[agentName]

		// Wait for on_event gates (session mode). In topo order the emitter
		// always precedes the subscriber, so WaitGates normally returns
		// immediately (channel already fired). The guard is still needed for
		// robustness and for the fan-in case.
		if gw != nil {
			if err := gw.WaitGates(ctx, agentName, ea); err != nil {
				return err
			}
		}

		if err := o.ensureAgent(ctx, agentName, ea); err != nil {
			return err
		}

		// Drive the agent session (event-driven mode only).
		if o.Driver != nil {
			sbxName := o.SandboxName(agentName)
			if gw != nil {
				if err := gw.StageSubscribed(ctx, agentName, sbxName, ea); err != nil {
					return err
				}
			}
			if err := o.driveSession(ctx, agentName, ea, reg, agents); err != nil {
				return err
			}
		}
	}

	logf(o.Out, "All agents running.\n")
	return nil
}

// driveSession opens an agent session, drives it to completion, and fires
// any channels the agent emits. Reach env vars (HAKO_REACH_*) are resolved
// before opening the session and forwarded in AgentSessionRequest.Env.
func (o *Orchestrator) driveSession(
	ctx context.Context,
	agentName string,
	ea *config.EffectiveAgent,
	reg *ChannelRegistry,
	agents map[string]*config.EffectiveAgent,
) error {
	sbxName := o.SandboxName(agentName)
	logf(o.Out, "[%s] attaching agent session on %s\n", agentName, sbxName)

	// Resolve reach env vars before opening the session.
	reachEnv, err := o.ApplyReach(ctx, agentName, ea, agents)
	if err != nil {
		return err
	}

	var env *map[string]string
	if len(reachEnv) > 0 {
		env = &reachEnv
	}

	session, err := o.Driver.AttachAgentSession(ctx, sbxName, sandboxapi.AgentSessionRequest{
		Env: env,
	})
	if err != nil {
		return fmt.Errorf("[%s] attach session: %w", agentName, err)
	}

	det := &EmitDetector{
		Registry: reg,
		Stager:   o.Stager,
		Out:      o.Out,
	}
	return det.DriveAndEmit(ctx, agentName, sbxName, ea, session)
}

// ensureAgent find-or-creates, publishes ports, starts, and waits for a
// single agent sandbox to reach the running state.
func (o *Orchestrator) ensureAgent(ctx context.Context, agentName string, ea *config.EffectiveAgent) error {
	sbxName := o.SandboxName(agentName)
	logf(o.Out, "[%s] ensuring sandbox %s\u2026\n", agentName, sbxName)

	// 1. Find or create — inspect first; only call sbx create when absent.
	_, inspErr := o.Client.InspectSandbox(ctx, sbxName)
	if inspErr != nil {
		if !sandbox.IsNotFound(inspErr) {
			return fmt.Errorf("[%s] inspect: %w", agentName, inspErr)
		}
		logf(o.Out, "[%s] creating sandbox %s\n", agentName, sbxName)
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
		logf(o.Out, "[%s] sandbox %s already exists, reusing\n", agentName, sbxName)
	}

	// 2. Publish declared ports (idempotent diff against existing bindings).
	if len(ea.Ports) > 0 {
		if err := o.publishPorts(ctx, agentName, sbxName, ea.Ports); err != nil {
			return err
		}
	}

	// 3. Start the sandbox (the daemon is idempotent on already-running).
	logf(o.Out, "[%s] starting sandbox %s\n", agentName, sbxName)
	if _, err := o.Client.StartSandbox(ctx, sbxName); err != nil {
		return fmt.Errorf("[%s] start: %w", agentName, err)
	}

	// 4. Poll until status == running.
	logf(o.Out, "[%s] waiting for running\u2026\n", agentName)
	if err := o.waitRunning(ctx, agentName, sbxName); err != nil {
		return err
	}

	logf(o.Out, "[%s] running \u2713\n", agentName)
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
		logf(o.Out, "[%s] all ports already published, skipping\n", agentName)
		return nil
	}

	logf(o.Out, "[%s] publishing %d port(s)\n", agentName, len(toPublish))
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
