package orchestrator

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
)

// defaultOnEventTimeout is the per-edge wait limit when no project/agent override
// is configured. Chosen to be long enough for real agent sessions (minutes) while
// still surfacing hangs instead of waiting forever.
const defaultOnEventTimeout = 30 * time.Minute

// GateWaiter holds the state needed to wait for on_event gates and stage
// subscribed payloads into a subscriber sandbox before its session starts.
type GateWaiter struct {
	Registry *ChannelRegistry
	Stager   sandbox.FileStager
	Out      io.Writer
	// OnEventTimeout is the per-edge timeout. Zero means defaultOnEventTimeout.
	OnEventTimeout time.Duration
}

// WaitGates blocks until all on_event conditions in ea.DependsOn have fired.
// For non-on_event conditions (created/running/completed) it returns immediately
// (those are satisfied by the Up topo-walk before DriveAndEmit is called).
//
// Returns an error if any channel times out without firing, naming the stalled
// channel and its configured emitter.
func (g *GateWaiter) WaitGates(ctx context.Context, agentName string, ea *config.EffectiveAgent) error {
	timeout := g.OnEventTimeout
	if timeout == 0 {
		timeout = defaultOnEventTimeout
	}

	for depName, dep := range ea.DependsOn {
		if dep.Condition != config.ConditionOnEvent {
			continue
		}
		ch := dep.Channel
		if ch == "" {
			return fmt.Errorf("[%s] on_event depends_on %q has no channel set", agentName, depName)
		}

		if g.Registry.IsFired(ch) {
			continue // already fired — nothing to wait
		}

		logf(g.Out, "[%s] waiting for channel %q (emitter: %s)…\n",
			agentName, ch, g.Registry.Emitter(ch))

		fired := g.Registry.WaitFired(ch)
		if fired == nil {
			return fmt.Errorf("[%s] channel %q is not registered in the registry", agentName, ch)
		}

		deadline := time.After(timeout)
		select {
		case <-fired:
			logf(g.Out, "[%s] channel %q fired ✓\n", agentName, ch)
		case <-deadline:
			return fmt.Errorf(
				"[%s] timed out waiting for channel %q to fire (emitter: %s, timeout: %s)",
				agentName, ch, g.Registry.Emitter(ch), timeout,
			)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// StageSubscribed stages every channel in ea.Subscribes into the subscriber's
// sandbox at .hako/in/<channel>.json just before the agent session starts.
// It reads the payload from the registry (channels must be fired before this
// is called — WaitGates ensures that for on_event subscriptions).
func (g *GateWaiter) StageSubscribed(ctx context.Context, agentName, sbxName string, ea *config.EffectiveAgent) error {
	for _, ch := range ea.Subscribes {
		payload, ok := g.Registry.Payload(ch)
		if !ok {
			// The channel is subscribed but not fired yet. This is only legal
			// if there is no on_event gate for this channel — in that case the
			// agent is expected to handle a missing in-file gracefully.
			// Log a warning and skip staging.
			logf(g.Out, "[%s] warning: channel %q not yet fired; skipping staging of in-payload\n",
				agentName, ch)
			continue
		}

		inPath := sandbox.HakoInPath(ch)
		logf(g.Out, "[%s] staging payload for channel %q at %s (%d bytes)\n",
			agentName, ch, inPath, len(payload))

		if err := g.Stager.PutFile(ctx, sbxName, inPath, []byte(payload)); err != nil {
			return fmt.Errorf("[%s] stage payload for channel %q: %w", agentName, ch, err)
		}
	}
	return nil
}
