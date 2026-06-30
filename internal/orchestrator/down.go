package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// Down stops and removes all sandboxes belonging to the project, unpublishes
// their ports, and removes only the network-policy rules that Hakoniwa created
// (identified by hakoRulePrefix). Rules created by default, blueprints, or
// remote management are never touched.
//
// The operation is fully idempotent: a second Down on an already-torn-down
// project is a clean no-op.
func (o *Orchestrator) Down(ctx context.Context) error {
	prefix := o.ProjectName + "-"

	logf(o.Out, "Listing sandboxes for project %q…\n", o.ProjectName)

	all, err := o.Client.ListSandboxes(ctx)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}

	// Filter to this project's sandboxes.
	var ours []sandbox.SandboxInfo
	for _, info := range all {
		if strings.HasPrefix(info.Name, prefix) {
			ours = append(ours, info)
		}
	}

	if len(ours) == 0 {
		logf(o.Out, "No sandboxes found for project %q — nothing to do.\n", o.ProjectName)
		return nil
	}

	for _, info := range ours {
		if err := o.teardownSandbox(ctx, info.Name); err != nil {
			return err
		}
	}

	logf(o.Out, "Project %q torn down.\n", o.ProjectName)
	return nil
}

// teardownSandbox stops, unpublishes, and deletes a single sandbox.
func (o *Orchestrator) teardownSandbox(ctx context.Context, sbxName string) error {
	logf(o.Out, "[%s] stopping…\n", sbxName)

	// Stop (idempotent — ignore NotFound).
	if _, err := o.Client.StopSandbox(ctx, sbxName); err != nil {
		if !sandbox.IsNotFound(err) {
			return fmt.Errorf("[%s] stop: %w", sbxName, err)
		}
	}

	// Unpublish all ports — list first so we know what to remove.
	ports, err := o.Client.ListPublishedPorts(ctx, sbxName)
	if err != nil && !sandbox.IsNotFound(err) {
		return fmt.Errorf("[%s] list ports: %w", sbxName, err)
	}
	if len(ports) > 0 {
		keys := make([]sandbox.PortKey, len(ports))
		for i, p := range ports {
			hp := p.HostPort
			proto := sandboxapi.PortKeyProtocol(string(p.Protocol))
			keys[i] = sandbox.PortKey{
				SandboxPort: p.SandboxPort,
				HostPort:    &hp,
				Protocol:    &proto,
			}
		}
		logf(o.Out, "[%s] unpublishing %d port(s)\n", sbxName, len(keys))
		if err := o.Client.UnpublishPorts(ctx, sbxName, keys); err != nil && !sandbox.IsNotFound(err) {
			return fmt.Errorf("[%s] unpublish ports: %w", sbxName, err)
		}
	}

	// Delete the sandbox.
	logf(o.Out, "[%s] deleting…\n", sbxName)
	if err := o.Client.DeleteSandbox(ctx, sbxName); err != nil {
		if !sandbox.IsNotFound(err) {
			return fmt.Errorf("[%s] delete: %w", sbxName, err)
		}
		// Already gone — idempotent.
		logf(o.Out, "[%s] already removed\n", sbxName)
	} else {
		logf(o.Out, "[%s] removed ✓\n", sbxName)
	}

	return nil
}
