package orchestrator

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newGateWaiter(reg *ChannelRegistry, stager sandbox.FileStager, timeout time.Duration) *GateWaiter {
	return &GateWaiter{
		Registry:       reg,
		Stager:         stager,
		Out:            io.Discard,
		OnEventTimeout: timeout,
	}
}

func eaWithDependsOn(ch, emitter string, subscribes []string) *config.EffectiveAgent {
	return &config.EffectiveAgent{
		DependsOn: map[string]config.DependsOnEntry{
			emitter: {Condition: config.ConditionOnEvent, Channel: ch},
		},
		Subscribes: subscribes,
	}
}

// ---------------------------------------------------------------------------
// WaitGates tests
// ---------------------------------------------------------------------------

func TestWaitGatesAlreadyFired(t *testing.T) {
	reg := NewChannelRegistry([]string{"ready"}, map[string]string{"ready": "alpha"})
	_ = reg.Fire("ready", nil)

	gw := newGateWaiter(reg, nil, time.Second)
	ea := eaWithDependsOn("ready", "alpha", nil)

	if err := gw.WaitGates(context.Background(), "consumer", ea); err != nil {
		t.Fatalf("WaitGates (pre-fired): %v", err)
	}
}

func TestWaitGatesFiresBeforeTimeout(t *testing.T) {
	reg := NewChannelRegistry([]string{"sig"}, map[string]string{"sig": "producer"})

	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = reg.Fire("sig", nil)
	}()

	gw := newGateWaiter(reg, nil, 3*time.Second)
	ea := eaWithDependsOn("sig", "producer", nil)

	if err := gw.WaitGates(context.Background(), "consumer", ea); err != nil {
		t.Fatalf("WaitGates: %v", err)
	}
}

func TestWaitGatesTimeout(t *testing.T) {
	reg := NewChannelRegistry([]string{"never"}, map[string]string{"never": "ghost"})

	gw := newGateWaiter(reg, nil, 30*time.Millisecond) // very short
	ea := eaWithDependsOn("never", "ghost", nil)

	err := gw.WaitGates(context.Background(), "consumer", ea)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected 'timed out' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "never") {
		t.Errorf("expected channel name in error, got: %v", err)
	}
}

func TestWaitGatesContextCancelled(t *testing.T) {
	reg := NewChannelRegistry([]string{"blocked"}, nil)

	gw := newGateWaiter(reg, nil, 5*time.Second)
	ea := eaWithDependsOn("blocked", "nobody", nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := gw.WaitGates(ctx, "consumer", ea)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestWaitGatesNonOnEventSkipped(t *testing.T) {
	reg := NewChannelRegistry(nil, nil) // no channels needed
	gw := newGateWaiter(reg, nil, time.Second)

	// Non-on_event conditions should be skipped entirely.
	ea := &config.EffectiveAgent{
		DependsOn: map[string]config.DependsOnEntry{
			"dep": {Condition: config.ConditionRunning},
		},
	}
	if err := gw.WaitGates(context.Background(), "consumer", ea); err != nil {
		t.Fatalf("WaitGates with running condition: %v", err)
	}
}

func TestWaitGatesFanIn(t *testing.T) {
	reg := NewChannelRegistry([]string{"ch1", "ch2"}, nil)
	_ = reg.Fire("ch1", nil)

	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = reg.Fire("ch2", nil)
	}()

	gw := newGateWaiter(reg, nil, 3*time.Second)
	ea := &config.EffectiveAgent{
		DependsOn: map[string]config.DependsOnEntry{
			"a": {Condition: config.ConditionOnEvent, Channel: "ch1"},
			"b": {Condition: config.ConditionOnEvent, Channel: "ch2"},
		},
	}
	if err := gw.WaitGates(context.Background(), "joiner", ea); err != nil {
		t.Fatalf("WaitGates (fan-in): %v", err)
	}
}

// ---------------------------------------------------------------------------
// StageSubscribed tests
// ---------------------------------------------------------------------------

func TestStageSubscribedPayload(t *testing.T) {
	reg := NewChannelRegistry([]string{"ready"}, nil)
	payload := []byte(`{"status":"done"}`)
	_ = reg.Fire("ready", payload)

	stager := newFakeStager()
	gw := newGateWaiter(reg, stager, time.Second)

	ea := &config.EffectiveAgent{Subscribes: []string{"ready"}}
	if err := gw.StageSubscribed(context.Background(), "consumer", "proj-consumer", ea); err != nil {
		t.Fatalf("StageSubscribed: %v", err)
	}

	k := stager.key("proj-consumer", sandbox.HakoInPath("ready"))
	staged := stager.staged[k]
	if string(staged) != string(payload) {
		t.Errorf("staged payload = %q, want %q", staged, payload)
	}
}

func TestStageSubscribedUnfiredSkipped(t *testing.T) {
	reg := NewChannelRegistry([]string{"ch"}, nil) // not fired
	stager := newFakeStager()
	gw := newGateWaiter(reg, stager, time.Second)

	ea := &config.EffectiveAgent{Subscribes: []string{"ch"}}
	// Should succeed without error (logs a warning, skips staging).
	if err := gw.StageSubscribed(context.Background(), "consumer", "proj-consumer", ea); err != nil {
		t.Fatalf("StageSubscribed (unfired): %v", err)
	}
	if len(stager.staged) != 0 {
		t.Error("should not have staged anything for unfired channel")
	}
}

func TestStageSubscribedMultipleChannels(t *testing.T) {
	reg := NewChannelRegistry([]string{"ch1", "ch2"}, nil)
	_ = reg.Fire("ch1", []byte(`"p1"`))
	_ = reg.Fire("ch2", []byte(`"p2"`))

	stager := newFakeStager()
	gw := newGateWaiter(reg, stager, time.Second)

	ea := &config.EffectiveAgent{Subscribes: []string{"ch1", "ch2"}}
	if err := gw.StageSubscribed(context.Background(), "consumer", "proj-c", ea); err != nil {
		t.Fatalf("StageSubscribed: %v", err)
	}
	if len(stager.staged) != 2 {
		t.Errorf("expected 2 staged files, got %d", len(stager.staged))
	}
}

func TestStageSubscribedEmpty(t *testing.T) {
	gw := newGateWaiter(nil, nil, time.Second)
	ea := &config.EffectiveAgent{}
	if err := gw.StageSubscribed(context.Background(), "a", "proj-a", ea); err != nil {
		t.Fatalf("empty Subscribes: %v", err)
	}
}
