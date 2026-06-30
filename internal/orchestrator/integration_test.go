package orchestrator_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/orchestrator"
	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/fake"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newOrch builds an Orchestrator wired to a single fake.Backend that
// satisfies all four required interfaces.
func newOrch(b *fake.Backend, projectName string) *orchestrator.Orchestrator {
	orch, err := orchestrator.NewOrchestrator(b, b, projectName, io.Discard)
	if err != nil {
		panic("newOrch: " + err.Error())
	}
	orch.Driver = b
	orch.Stager = b
	return orch
}

// chainProject returns a 3-agent project A→B→C with on_event channels.
func chainProject() *config.Project {
	return &config.Project{
		Name:     "integ",
		Channels: []string{"a.done", "b.done"},
		Agents: map[string]*config.Agent{
			"a": {Agent: "shell", Emits: []string{"a.done"}},
			"b": {
				Agent:      "shell",
				Subscribes: []string{"a.done"},
				DependsOn:  map[string]config.DependsOnEntry{"a": {Condition: config.ConditionOnEvent, Channel: "a.done"}},
				Emits:      []string{"b.done"},
			},
			"c": {
				Agent:      "shell",
				Subscribes: []string{"b.done"},
				DependsOn:  map[string]config.DependsOnEntry{"b": {Condition: config.ConditionOnEvent, Channel: "b.done"}},
			},
		},
	}
}

// configureChain seeds the backend with session configs for A, B, C so that
// each emits its channel payload.
func configureChain(b *fake.Backend) {
	b.ConfigureSession("integ-a", &fake.SessionConfig{
		Output: "agent-a output\n",
		Emits:  map[string]json.RawMessage{"a.done": json.RawMessage(`{"from":"a"}`)},
	})
	b.ConfigureSession("integ-b", &fake.SessionConfig{
		Output: "agent-b output\n",
		Emits:  map[string]json.RawMessage{"b.done": json.RawMessage(`{"from":"b"}`)},
	})
	b.ConfigureSession("integ-c", &fake.SessionConfig{
		Output: "agent-c output\n",
	})
}

// ---------------------------------------------------------------------------
// Test: Up A→B→C linear chain with on_event events
// ---------------------------------------------------------------------------

func TestIntegrationUpLinearChain(t *testing.T) {
	b := fake.NewBackend()
	configureChain(b)
	orch := newOrch(b, "integ")
	p := chainProject()

	if err := orch.Up(context.Background(), p); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// All three sandboxes exist and are running.
	for _, name := range []string{"integ-a", "integ-b", "integ-c"} {
		if b.SandboxStatus(name) != "running" {
			t.Errorf("%s status = %q, want running", name, b.SandboxStatus(name))
		}
	}

	// Each sandbox was created exactly once.
	for _, name := range []string{"integ-a", "integ-b", "integ-c"} {
		if n := b.CreateCalls(name); n != 1 {
			t.Errorf("%s: create calls = %d, want 1", name, n)
		}
	}

	// Channel payloads were staged for subscribers.
	bInFile := b.ReadFile("integ-b", sandbox.HakoInPath("a.done"))
	if bInFile == nil {
		t.Error("integ-b: .hako/in/a.done.json not staged")
	}
	cInFile := b.ReadFile("integ-c", sandbox.HakoInPath("b.done"))
	if cInFile == nil {
		t.Error("integ-c: .hako/in/b.done.json not staged")
	}
}

// ---------------------------------------------------------------------------
// Test: second Up is idempotent (no extra creates)
// ---------------------------------------------------------------------------

func TestIntegrationUpIdempotent(t *testing.T) {
	b := fake.NewBackend()
	configureChain(b)
	orch := newOrch(b, "integ")
	p := chainProject()

	if err := orch.Up(context.Background(), p); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if err := orch.Up(context.Background(), p); err != nil {
		t.Fatalf("second Up: %v", err)
	}

	// Create must not have been called a second time.
	for _, name := range []string{"integ-a", "integ-b", "integ-c"} {
		if n := b.CreateCalls(name); n != 1 {
			t.Errorf("%s: create calls after 2× Up = %d, want 1", name, n)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: Down removes project sandboxes; second Down is no-op
// ---------------------------------------------------------------------------

func TestIntegrationDownIdempotent(t *testing.T) {
	b := fake.NewBackend()
	configureChain(b)
	orch := newOrch(b, "integ")
	p := chainProject()

	if err := orch.Up(context.Background(), p); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Down removes all three project sandboxes.
	if err := orch.Down(context.Background()); err != nil {
		t.Fatalf("first Down: %v", err)
	}
	for _, name := range []string{"integ-a", "integ-b", "integ-c"} {
		if s := b.SandboxStatus(name); s != "" {
			t.Errorf("%s still present after Down (status=%q)", name, s)
		}
	}

	// Second Down on an empty project is a clean no-op.
	if err := orch.Down(context.Background()); err != nil {
		t.Fatalf("second Down: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test: Plan on a fresh project lists all agents as "create"
// ---------------------------------------------------------------------------

func TestIntegrationPlanFresh(t *testing.T) {
	b := fake.NewBackend()
	orch := newOrch(b, "integ")
	// Don't set Driver/Stager so Plan stays read-only without session noise.
	orch.Driver = nil
	orch.Stager = nil

	p := &config.Project{
		Name:   "integ",
		Agents: map[string]*config.Agent{"x": {Agent: "shell"}, "y": {Agent: "shell"}},
	}

	entries, err := orch.Plan(context.Background(), p)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	for _, e := range entries {
		if e.Action != orchestrator.ActionCreate {
			t.Errorf("agent %s: action = %s, want create", e.Agent, e.Action)
		}
	}
	// Plan must not have created any sandboxes.
	for _, name := range []string{"integ-x", "integ-y"} {
		if b.CreateCalls(name) != 0 || b.SandboxStatus(name) != "" {
			t.Errorf("%s was created during Plan (plan must be read-only)", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: Plan on partially-existing project shows reuse for existing sandbox
// ---------------------------------------------------------------------------

func TestIntegrationPlanWithExisting(t *testing.T) {
	b := fake.NewBackend()
	orch := newOrch(b, "integ")
	orch.Driver = nil
	orch.Stager = nil

	p := &config.Project{
		Name:   "integ",
		Agents: map[string]*config.Agent{"x": {Agent: "shell"}, "y": {Agent: "shell"}},
	}

	// Pre-create integ-x so Plan sees it as existing.
	if err := b.Create(context.Background(), sandbox.CreateRequest{Name: "integ-x", Agent: "shell"}); err != nil {
		t.Fatal(err)
	}

	entries, err := orch.Plan(context.Background(), p)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	byAgent := make(map[string]orchestrator.AgentAction)
	for _, e := range entries {
		byAgent[e.Agent] = e.Action
	}

	if byAgent["x"] != orchestrator.ActionReuse {
		t.Errorf("x: action = %s, want reuse", byAgent["x"])
	}
	if byAgent["y"] != orchestrator.ActionCreate {
		t.Errorf("y: action = %s, want create", byAgent["y"])
	}
}

// ---------------------------------------------------------------------------
// Test: Up aborts when emitter session fails (exits non-zero)
// ---------------------------------------------------------------------------

func TestIntegrationUpEmitterFails(t *testing.T) {
	b := fake.NewBackend()
	b.ConfigureSession("integ-a", &fake.SessionConfig{
		ExitCode: 1, // fail — no output file written
	})
	// b and c are not configured; they should never run.
	orch := newOrch(b, "integ")
	p := chainProject()

	err := orch.Up(context.Background(), p)
	if err == nil {
		t.Fatal("expected error when emitter exits non-zero")
	}
	if !strings.Contains(err.Error(), "exited with code 1") {
		t.Errorf("unexpected error: %v", err)
	}
	// B and C must not have been created.
	if b.SandboxStatus("integ-b") != "" {
		t.Error("integ-b should not have been started after emitter failure")
	}
}
