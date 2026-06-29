package orchestrator

import (
	"context"
	"testing"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

func TestPlanFreshProject(t *testing.T) {
	s := newFakeState() // no sandboxes — all agents will be "create"
	o, _ := newTestOrchestrator(s, "proj")

	project := &config.Project{
		Name: "proj",
		Agents: map[string]*config.Agent{
			"alpha": {Agent: "claude"},
			"beta":  {Agent: "codex", Ports: []string{"8080:8080"}},
		},
	}

	entries, err := o.Plan(context.Background(), project)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Action != ActionCreate {
			t.Errorf("agent %s: action = %s, want create", e.Agent, e.Action)
		}
	}
}

func TestPlanReuseExistingNoPorts(t *testing.T) {
	s := newFakeState()
	s.sandboxes["proj-worker"] = &sandbox.SandboxInfo{
		Name: "proj-worker", Status: sandboxapi.SandboxInfoStatusRunning,
	}
	o, _ := newTestOrchestrator(s, "proj")

	project := minimalProject("proj", "worker", "claude")

	entries, err := o.Plan(context.Background(), project)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(entries) != 1 || entries[0].Action != ActionReuse {
		t.Errorf("expected reuse, got %+v", entries)
	}
}

func TestPlanConvergeMissingPort(t *testing.T) {
	s := newFakeState()
	// Sandbox exists but port 9000 is not yet published.
	s.sandboxes["proj-web"] = &sandbox.SandboxInfo{
		Name: "proj-web", Status: sandboxapi.SandboxInfoStatusRunning,
	}
	o, _ := newTestOrchestrator(s, "proj")

	project := minimalProject("proj", "web", "claude")
	project.Agents["web"].Ports = []string{"9000:9000"}

	entries, err := o.Plan(context.Background(), project)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(entries) != 1 || entries[0].Action != ActionConverge {
		t.Errorf("expected converge, got %+v", entries)
	}
	if len(entries[0].Ports) != 1 || entries[0].Ports[0] != "9000:9000" {
		t.Errorf("missing ports = %v", entries[0].Ports)
	}
}

func TestPlanNoMutation(t *testing.T) {
	// Plan on a fresh project must not create any sandboxes.
	s := newFakeState()
	o, _ := newTestOrchestrator(s, "proj")

	project := minimalProject("proj", "worker", "claude")
	if _, err := o.Plan(context.Background(), project); err != nil {
		t.Fatal(err)
	}

	if len(s.sandboxes) != 0 {
		t.Error("Plan must not create any sandboxes")
	}
	if len(s.createCalls) != 0 {
		t.Error("Plan must not call sbx.Create")
	}
}

func TestPsEmpty(t *testing.T) {
	s := newFakeState()
	o, _ := newTestOrchestrator(s, "proj")

	entries, err := o.Ps(context.Background())
	if err != nil {
		t.Fatalf("Ps: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestPsFiltersProjectPrefix(t *testing.T) {
	s := newFakeState()
	s.sandboxes["proj-alpha"] = &sandbox.SandboxInfo{
		Name: "proj-alpha", Status: sandboxapi.SandboxInfoStatusRunning,
	}
	s.sandboxes["proj-beta"] = &sandbox.SandboxInfo{
		Name: "proj-beta", Status: sandboxapi.SandboxInfoStatusStopped,
	}
	s.sandboxes["other-gamma"] = &sandbox.SandboxInfo{
		Name: "other-gamma", Status: sandboxapi.SandboxInfoStatusRunning,
	}
	o, _ := newTestOrchestrator(s, "proj")

	entries, err := o.Ps(context.Background())
	if err != nil {
		t.Fatalf("Ps: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}
	// Sorted by name → alpha, beta.
	if entries[0].Agent != "alpha" || entries[1].Agent != "beta" {
		t.Errorf("unexpected order: %v, %v", entries[0].Agent, entries[1].Agent)
	}
	if entries[0].Status != "running" {
		t.Errorf("alpha status = %q, want running", entries[0].Status)
	}
}

func TestPsShowsPorts(t *testing.T) {
	s := newFakeState()
	s.sandboxes["proj-web"] = &sandbox.SandboxInfo{
		Name: "proj-web", Status: sandboxapi.SandboxInfoStatusRunning,
	}
	proto := sandboxapi.PublishedPortProtocol("tcp")
	s.publishedPorts["proj-web"] = []sandbox.PublishedPort{
		{HostIp: "127.0.0.1", HostPort: 8080, SandboxPort: 8080, Protocol: proto},
	}
	o, _ := newTestOrchestrator(s, "proj")

	entries, err := o.Ps(context.Background())
	if err != nil {
		t.Fatalf("Ps: %v", err)
	}
	if len(entries) != 1 || len(entries[0].Ports) != 1 {
		t.Fatalf("expected 1 entry with 1 port, got %+v", entries)
	}
	if entries[0].Ports[0] != "8080:8080/tcp" {
		t.Errorf("port = %q, want 8080:8080/tcp", entries[0].Ports[0])
	}
}
