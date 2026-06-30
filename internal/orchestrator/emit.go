package orchestrator

import (
	"context"
	"fmt"
	"io"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
)

// EmitDetector runs an agent session and fires the channels declared in the
// agent's emits list when the session completes. It reads the payload for each
// emitted channel from the sandbox's .hako/out/<channel>.json file via the
// FileStager. If a channel's output file is absent, it is treated as a failed
// emit and the returned error names both the channel and the emitter.
//
// Completion is detected by Session.Stream returning (stream EOF). After
// streaming, Session.ExitCode is called to recover the exit code; a non-zero
// exit is an error that suppresses emit detection (the agent did not succeed).
type EmitDetector struct {
	Registry *ChannelRegistry
	Stager   sandbox.FileStager
	Out      io.Writer
}

// DriveAndEmit drives the agent session for agentName (sandbox sbxName) and,
// on clean completion (exit code 0), fires every channel in emits via the
// registry. Output is streamed live to d.Out.
//
// Error semantics:
//   - session exit code ≠ 0: error "agent <name> exited with code <n>"
//   - output file absent for a required channel: error naming channel+emitter
//   - registry.Fire fails: error
func (d *EmitDetector) DriveAndEmit(
	ctx context.Context,
	agentName, sbxName string,
	ea *config.EffectiveAgent,
	session sandbox.Session,
) error {
	defer func() { _ = session.Close() }()

	_, _ = fmt.Fprintf(d.Out, "[%s] streaming session…\n", agentName)

	// Stream the agent output (blocks until session ends).
	if err := session.Stream(d.Out, d.Out); err != nil {
		return fmt.Errorf("[%s] session stream error: %w", agentName, err)
	}

	_, _ = fmt.Fprintf(d.Out, "[%s] session ended; checking exit code…\n", agentName)

	// Recover exit code via InspectExec.
	exitCode, err := session.ExitCode(ctx)
	if err != nil {
		return fmt.Errorf("[%s] inspect exit code: %w", agentName, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("[%s] agent session exited with code %d", agentName, exitCode)
	}

	// Fire each emitted channel.
	for _, ch := range ea.Emits {
		if err := d.fireChannel(ctx, agentName, sbxName, ch); err != nil {
			return err
		}
	}
	return nil
}

// fireChannel reads the channel's output payload from the sandbox filesystem
// and fires the channel in the registry.
func (d *EmitDetector) fireChannel(ctx context.Context, agentName, sbxName, ch string) error {
	path := sandbox.HakoOutPath(ch)
	_, _ = fmt.Fprintf(d.Out, "[%s] reading emit payload for channel %q from %s\n", agentName, ch, path)

	payload, err := d.Stager.GetFile(ctx, sbxName, path)
	if err != nil {
		if sandbox.IsNotFound(err) {
			return fmt.Errorf(
				"[%s] channel %q not emitted: output file %q is absent — "+
					"agent must write this file before exiting",
				agentName, ch, path,
			)
		}
		return fmt.Errorf("[%s] read emit payload for channel %q: %w", agentName, ch, err)
	}

	if err := d.Registry.Fire(ch, payload); err != nil {
		return fmt.Errorf("[%s] fire channel %q: %w", agentName, ch, err)
	}

	_, _ = fmt.Fprintf(d.Out, "[%s] channel %q fired ✓ (%d bytes)\n", agentName, ch, len(payload))
	return nil
}
