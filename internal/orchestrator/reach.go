package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// reachHostname is the hostname sandboxes use to address the host.
// On Docker Desktop (Linux and macOS) `host.docker.internal` resolves to the
// host's gateway address from inside the container/VM. This is the Phase-1
// MVP mechanism; Phase-2 will use a private inter-sandbox bridge instead.
const reachHostname = "host.docker.internal"

// ApplyReach publishes the ports required by ea.Reach and returns an env-var
// map that must be passed to the consumer's AgentSessionRequest.Env so the
// agent can address each reachable service.
//
// For each "targetAgent:sandboxPort" in ea.Reach:
//  1. Ensure the target sandbox has sandboxPort published (publish if absent).
//  2. Read the resulting host port (auto-assigned or declared).
//  3. Record HAKO_REACH_<TARGET>_<PORT>=host.docker.internal:<hostport>.
//
// The caller (driveSession) merges the returned map into the session env.
func (o *Orchestrator) ApplyReach(
	ctx context.Context,
	agentName string,
	ea *config.EffectiveAgent,
	agents map[string]*config.EffectiveAgent,
) (map[string]string, error) {
	if len(ea.Reach) == 0 {
		return nil, nil
	}

	env := make(map[string]string, len(ea.Reach))

	for _, r := range ea.Reach {
		colon := strings.LastIndex(r, ":")
		if colon < 0 {
			return nil, fmt.Errorf("[%s] malformed reach entry %q", agentName, r)
		}
		targetAgent := r[:colon]
		sandboxPortStr := r[colon+1:]

		targetSbx := o.SandboxName(targetAgent)
		targetEA := agents[targetAgent]

		hostPort, err := o.ensureReachPort(ctx, agentName, targetSbx, sandboxPortStr, targetEA)
		if err != nil {
			return nil, err
		}

		// Build env var name: HAKO_REACH_<TARGET>_<PORT>
		// Normalise: uppercase, replace '-' and '.' with '_'.
		envName := fmt.Sprintf("HAKO_REACH_%s_%s",
			normaliseEnvSegment(targetAgent),
			normaliseEnvSegment(sandboxPortStr),
		)
		env[envName] = fmt.Sprintf("%s:%d", reachHostname, hostPort)

		logf(o.Out, "[%s] reach %s -> %s=%s\n",
			agentName, r, envName, env[envName])
	}
	return env, nil
}

// ensureReachPort publishes sandboxPort on the target sandbox if not already
// published, and returns the resulting host port.
func (o *Orchestrator) ensureReachPort(
	ctx context.Context,
	consumerAgent, targetSbx, sandboxPortStr string,
	targetEA *config.EffectiveAgent,
) (int, error) {
	// Parse the sandbox port.
	var sbxPort int
	if _, err := fmt.Sscanf(sandboxPortStr, "%d", &sbxPort); err != nil {
		return 0, fmt.Errorf("[%s] reach: invalid sandbox port %q: %w", consumerAgent, sandboxPortStr, err)
	}

	// List current published ports for the target sandbox.
	existing, err := o.Client.ListPublishedPorts(ctx, targetSbx)
	if err != nil && !sandbox.IsNotFound(err) {
		return 0, fmt.Errorf("[%s] reach: list ports for %s: %w", consumerAgent, targetSbx, err)
	}

	// Check if already published.
	for _, p := range existing {
		if p.SandboxPort == sbxPort {
			return p.HostPort, nil
		}
	}

	// Determine the host port from the target agent's declared ports, or auto-assign (0).
	hostPort := resolveHostPort(targetEA, sbxPort)

	logf(o.Out, "[%s] reach: publishing port %d on %s\n", consumerAgent, sbxPort, targetSbx)

	proto := sandboxapi.PortPublishRequestProtocol("tcp")
	published, err := o.Client.PublishPorts(ctx, targetSbx, []sandbox.PortPublishRequest{
		{SandboxPort: sbxPort, HostPort: hostPort, Protocol: &proto},
	})
	if err != nil {
		return 0, fmt.Errorf("[%s] reach: publish port %d on %s: %w",
			consumerAgent, sbxPort, targetSbx, err)
	}
	if len(published) == 0 {
		return 0, fmt.Errorf("[%s] reach: no binding returned for port %d on %s",
			consumerAgent, sbxPort, targetSbx)
	}
	return published[0].HostPort, nil
}

// resolveHostPort returns the declared host port for a given sandbox port in
// ea.Ports, or 0 (auto-assign) if not found.
func resolveHostPort(ea *config.EffectiveAgent, sbxPort int) int {
	if ea == nil {
		return 0
	}
	for _, spec := range ea.Ports {
		req, err := sandbox.ParsePortSpec(spec)
		if err != nil {
			continue
		}
		if req.SandboxPort == sbxPort && req.HostPort != 0 {
			return req.HostPort
		}
	}
	return 0
}

// normaliseEnvSegment converts a string to an uppercase env-var-safe segment
// by replacing hyphens, dots, and slashes with underscores and uppercasing.
func normaliseEnvSegment(s string) string {
	s = strings.ToUpper(s)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	s = strings.ReplaceAll(s, "/", "_")
	return s
}
