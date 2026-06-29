package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// ---------------------------------------------------------------------------
// fakeDriver — implements sandbox.SessionDriver for executor tests
// ---------------------------------------------------------------------------

// fakeDriverState records calls and provides configurable session results.
type fakeDriverState struct {
	// sessionResults maps sbxName -> fakeSession to return.
	sessionResults map[string]*fakeSession
	attached       []string // ordered list of sbxNames that were attached
}

func newFakeDriverState() *fakeDriverState {
	return &fakeDriverState{
		sessionResults: make(map[string]*fakeSession),
	}
}

type fakeDriver struct {
	state *fakeDriverState
}

func (d *fakeDriver) AttachAgentSession(_ context.Context, name string, _ sandboxapi.AgentSessionRequest) (sandbox.Session, error) {
	d.state.attached = append(d.state.attached, name)
	sess := d.state.sessionResults[name]
	if sess == nil {
		sess = &fakeSession{exitCode: 0}
	}
	return sess, nil
}

// ---------------------------------------------------------------------------
// newTestOrchestratorWithDriver — builds an Orchestrator with all fakes
// ---------------------------------------------------------------------------

func newTestOrchestratorWithDriver(s *fakeState, ds *fakeDriverState, projectName string) (*Orchestrator, error) {
	orch, err := newTestOrchestrator(s, projectName)
	if err != nil {
		return nil, err
	}
	stager := newFakeStager()
	orch.Driver = &fakeDriver{state: ds}
	orch.Stager = stager
	return orch, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestExecutorLinearChain(t *testing.T) {
	// A -> B (on_event ch.a), B -> C (on_event ch.b)
	s := newFakeState()
	ds := newFakeDriverState()

	// Configure sessions: A and B each write their output file.
	stager := newFakeStager()
	stager.setFile("proj-a", sandbox.HakoOutPath("ch.a"), json.RawMessage(`"a-done"`))
	stager.setFile("proj-b", sandbox.HakoOutPath("ch.b"), json.RawMessage(`"b-done"`))

	orch, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}
	orch.Driver = &fakeDriver{state: ds}
	orch.Stager = stager

	project := &config.Project{
		Name:     "proj",
		Channels: []string{"ch.a", "ch.b"},
		Agents: map[string]*config.Agent{
			"a": {Agent: "shell", Emits: []string{"ch.a"}},
			"b": {
				Agent:      "shell",
				Emits:      []string{"ch.b"},
				Subscribes: []string{"ch.a"},
				DependsOn: map[string]config.DependsOnEntry{
					"a": {Condition: config.ConditionOnEvent, Channel: "ch.a"},
				},
			},
			"c": {
				Agent:      "shell",
				Subscribes: []string{"ch.b"},
				DependsOn: map[string]config.DependsOnEntry{
					"b": {Condition: config.ConditionOnEvent, Channel: "ch.b"},
				},
			},
		},
	}

	if err := orch.Up(context.Background(), project); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// All three sandboxes started and sessions driven.
	if len(ds.attached) != 3 {
		t.Errorf("expected 3 sessions attached, got %d: %v", len(ds.attached), ds.attached)
	}
	// Order: a must come before b, b must come before c.
	aIdx, bIdx, cIdx := -1, -1, -1
	for i, n := range ds.attached {
		switch n {
		case "proj-a":
			aIdx = i
		case "proj-b":
			bIdx = i
		case "proj-c":
			cIdx = i
		}
	}
	if aIdx < 0 || bIdx < 0 || cIdx < 0 {
		t.Errorf("not all agents drove a session: %v", ds.attached)
	}
	if !(aIdx < bIdx && bIdx < cIdx) {
		t.Errorf("wrong order: a=%d b=%d c=%d (attached: %v)", aIdx, bIdx, cIdx, ds.attached)
	}
}

func TestExecutorFanOutFanIn(t *testing.T) {
	// producer -> {reviewerA, reviewerB} -> joiner (fan-out then fan-in)
	s := newFakeState()
	ds := newFakeDriverState()

	stager := newFakeStager()
	stager.setFile("proj-producer", sandbox.HakoOutPath("ready"), json.RawMessage(`"produced"`))
	stager.setFile("proj-reviewer-a", sandbox.HakoOutPath("review.a"), json.RawMessage(`"ra"`))
	stager.setFile("proj-reviewer-b", sandbox.HakoOutPath("review.b"), json.RawMessage(`"rb"`))

	orch, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}
	orch.Driver = &fakeDriver{state: ds}
	orch.Stager = stager

	project := &config.Project{
		Name:     "proj",
		Channels: []string{"ready", "review.a", "review.b"},
		Agents: map[string]*config.Agent{
			"producer": {
				Agent: "shell",
				Emits: []string{"ready"},
			},
			"reviewer-a": {
				Agent:      "shell",
				Subscribes: []string{"ready"},
				Emits:      []string{"review.a"},
				DependsOn: map[string]config.DependsOnEntry{
					"producer": {Condition: config.ConditionOnEvent, Channel: "ready"},
				},
			},
			"reviewer-b": {
				Agent:      "shell",
				Subscribes: []string{"ready"},
				Emits:      []string{"review.b"},
				DependsOn: map[string]config.DependsOnEntry{
					"producer": {Condition: config.ConditionOnEvent, Channel: "ready"},
				},
			},
			"joiner": {
				Agent:      "shell",
				Subscribes: []string{"review.a", "review.b"},
				DependsOn: map[string]config.DependsOnEntry{
					"reviewer-a": {Condition: config.ConditionOnEvent, Channel: "review.a"},
					"reviewer-b": {Condition: config.ConditionOnEvent, Channel: "review.b"},
				},
			},
		},
	}

	if err := orch.Up(context.Background(), project); err != nil {
		t.Fatalf("Up fan-out/fan-in: %v", err)
	}

	// All four agents drove sessions.
	if len(ds.attached) != 4 {
		t.Errorf("expected 4 sessions, got %d: %v", len(ds.attached), ds.attached)
	}
	// producer must come first; joiner must come last.
	first, last := ds.attached[0], ds.attached[len(ds.attached)-1]
	if first != "proj-producer" {
		t.Errorf("expected producer first, got %q", first)
	}
	if last != "proj-joiner" {
		t.Errorf("expected joiner last, got %q", last)
	}
}

func TestExecutorNoSessionDriver(t *testing.T) {
	// Infrastructure-only: Driver = nil, sessions not driven (backward compat).
	s := newFakeState()
	orch, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}
	// Driver is nil — session driving skipped.

	project := minimalProject("proj", "worker", "claude")
	if err := orch.Up(context.Background(), project); err != nil {
		t.Fatalf("Up without driver: %v", err)
	}

	if _, ok := s.sandboxes["proj-worker"]; !ok {
		t.Error("sandbox should have been created")
	}
}

func TestExecutorChannelTimeoutAborts(t *testing.T) {
	// The consumer depends on a channel that is never fired (emitter session
	// is misconfigured so the file is absent -> DriveAndEmit returns error
	// before firing). Since we use a sequential walk, the emitter's
	// DriveAndEmit will fail, and Up returns an error before the consumer
	// is even started.
	s := newFakeState()
	ds := newFakeDriverState()
	stager := newFakeStager() // no output file -> emit detection fails

	orch, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}
	orch.Driver = &fakeDriver{state: ds}
	orch.Stager = stager

	project := &config.Project{
		Name:     "proj",
		Channels: []string{"done"},
		Agents: map[string]*config.Agent{
			"emitter": {Agent: "shell", Emits: []string{"done"}},
			"consumer": {
				Agent:      "shell",
				Subscribes: []string{"done"},
				DependsOn: map[string]config.DependsOnEntry{
					"emitter": {Condition: config.ConditionOnEvent, Channel: "done"},
				},
			},
		},
	}

	err = orch.Up(context.Background(), project)
	if err == nil {
		t.Fatal("expected error when emitter fails to produce output file")
	}
	if !strings.Contains(err.Error(), "absent") && !strings.Contains(err.Error(), "done") {
		t.Errorf("expected 'absent' or channel name in error, got: %v", err)
	}
	// Consumer should not have been driven.
	for _, n := range ds.attached {
		if n == "proj-consumer" {
			t.Error("consumer should not have been driven after emitter failure")
		}
	}
}
