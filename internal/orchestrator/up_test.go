package orchestrator

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeState is shared between fakeClient and fakeSbx so both can mutate the
// same sandbox registry.
type fakeState struct {
	sandboxes      map[string]*sandbox.SandboxInfo
	publishedPorts map[string][]sandbox.PublishedPort
	createCalls    map[string]int
	// neverRun makes StartSandbox leave status as "stopped" (for timeout test).
	neverRun bool
}

func newFakeState() *fakeState {
	return &fakeState{
		sandboxes:      make(map[string]*sandbox.SandboxInfo),
		publishedPorts: make(map[string][]sandbox.PublishedPort),
		createCalls:    make(map[string]int),
	}
}

// fakeClient implements sandbox.Client backed by fakeState.
type fakeClient struct{ s *fakeState }

func (c *fakeClient) ListSandboxes(_ context.Context) ([]sandbox.SandboxInfo, error) {
	out := make([]sandbox.SandboxInfo, 0, len(c.s.sandboxes))
	for _, v := range c.s.sandboxes {
		out = append(out, *v)
	}
	return out, nil
}

func (c *fakeClient) InspectSandbox(_ context.Context, name string) (*sandbox.SandboxInfo, error) {
	if info, ok := c.s.sandboxes[name]; ok {
		cp := *info
		return &cp, nil
	}
	return nil, &sandbox.NotFoundError{Resource: "sandbox " + name}
}

func (c *fakeClient) StartSandbox(_ context.Context, name string) (*sandbox.SandboxInfo, error) {
	info, ok := c.s.sandboxes[name]
	if !ok {
		return nil, &sandbox.NotFoundError{Resource: "sandbox " + name}
	}
	if !c.s.neverRun {
		info.Status = sandboxapi.SandboxInfoStatusRunning
	}
	cp := *info
	return &cp, nil
}

func (c *fakeClient) StopSandbox(_ context.Context, name string) (*sandbox.SandboxInfo, error) {
	info, ok := c.s.sandboxes[name]
	if !ok {
		return nil, &sandbox.NotFoundError{Resource: "sandbox " + name}
	}
	info.Status = sandboxapi.SandboxInfoStatusStopped
	cp := *info
	return &cp, nil
}

func (c *fakeClient) DeleteSandbox(_ context.Context, name string) error {
	if _, ok := c.s.sandboxes[name]; !ok {
		return &sandbox.NotFoundError{Resource: "sandbox " + name}
	}
	delete(c.s.sandboxes, name)
	delete(c.s.publishedPorts, name)
	return nil
}

func (c *fakeClient) ListPublishedPorts(_ context.Context, name string) ([]sandbox.PublishedPort, error) {
	return append([]sandbox.PublishedPort(nil), c.s.publishedPorts[name]...), nil
}

func (c *fakeClient) PublishPorts(_ context.Context, name string, reqs []sandbox.PortPublishRequest) ([]sandbox.PublishedPort, error) {
	added := make([]sandbox.PublishedPort, 0, len(reqs))
	for _, r := range reqs {
		proto := sandboxapi.PublishedPortProtocol("tcp")
		if r.Protocol != nil {
			proto = sandboxapi.PublishedPortProtocol(string(*r.Protocol))
		}
		p := sandbox.PublishedPort{
			HostIp:      "127.0.0.1",
			HostPort:    r.HostPort,
			SandboxPort: r.SandboxPort,
			Protocol:    proto,
		}
		c.s.publishedPorts[name] = append(c.s.publishedPorts[name], p)
		added = append(added, p)
	}
	return added, nil
}

func (c *fakeClient) UnpublishPorts(_ context.Context, name string, keys []sandbox.PortKey) error {
	existing := c.s.publishedPorts[name]
	var kept []sandbox.PublishedPort
	for _, p := range existing {
		remove := false
		for _, k := range keys {
			if k.SandboxPort == p.SandboxPort {
				remove = true
				break
			}
		}
		if !remove {
			kept = append(kept, p)
		}
	}
	c.s.publishedPorts[name] = kept
	return nil
}

// fakeSbx implements sandbox.SbxAdapter backed by fakeState.
type fakeSbx struct{ s *fakeState }

func (f *fakeSbx) EnsureDaemon(_ context.Context) error { return nil }

func (f *fakeSbx) Create(_ context.Context, req sandbox.CreateRequest) error {
	f.s.createCalls[req.Name]++
	f.s.sandboxes[req.Name] = &sandbox.SandboxInfo{
		Name:   req.Name,
		Status: sandboxapi.SandboxInfoStatusStopped,
	}
	return nil
}

func (f *fakeSbx) SecretSetCustom(_ context.Context, _ string, _ sandbox.SecretSetRequest) error {
	return nil
}
func (f *fakeSbx) PolicySetDefault(_ context.Context, _ string) error     { return nil }
func (f *fakeSbx) PolicyAllow(_ context.Context, _, _ string) error       { return nil }
func (f *fakeSbx) PolicyDeny(_ context.Context, _, _ string) error        { return nil }
func (f *fakeSbx) PolicyRemove(_ context.Context, _, _ string) error      { return nil }
func (f *fakeSbx) List(_ context.Context) ([]sandbox.SbxListEntry, error) { return nil, nil }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newTestOrchestrator builds an Orchestrator with tiny poll settings for tests.
func newTestOrchestrator(s *fakeState, projectName string) (*Orchestrator, error) {
	o, err := NewOrchestrator(&fakeClient{s}, &fakeSbx{s}, projectName, io.Discard)
	if err != nil {
		return nil, err
	}
	o.PollInterval = time.Millisecond
	o.PollTimeout = 50 * time.Millisecond
	return o, nil
}

// minimalProject builds a project with a single no-dependency agent.
func minimalProject(name, agentName, agentKind string) *config.Project {
	return &config.Project{
		Name: name,
		Agents: map[string]*config.Agent{
			agentName: {Agent: agentKind},
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestUpSingleAgent(t *testing.T) {
	s := newFakeState()
	o, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}
	o.PollTimeout = 5 * time.Second

	project := minimalProject("proj", "worker", "claude")
	if err := o.Up(context.Background(), project); err != nil {
		t.Fatalf("Up: %v", err)
	}

	info, ok := s.sandboxes["proj-worker"]
	if !ok {
		t.Fatal("sandbox proj-worker not created")
	}
	if info.Status != sandboxapi.SandboxInfoStatusRunning {
		t.Errorf("status = %q, want running", info.Status)
	}
	if s.createCalls["proj-worker"] != 1 {
		t.Errorf("create calls = %d, want 1", s.createCalls["proj-worker"])
	}
}

func TestUpTwoAgentsDependency(t *testing.T) {
	s := newFakeState()
	o, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}
	o.PollTimeout = 5 * time.Second

	project := &config.Project{
		Name: "proj",
		Agents: map[string]*config.Agent{
			"alpha": {Agent: "claude"},
			"beta": {
				Agent: "codex",
				DependsOn: map[string]config.DependsOnEntry{
					"alpha": {Condition: config.ConditionRunning},
				},
			},
		},
	}

	if err := o.Up(context.Background(), project); err != nil {
		t.Fatalf("Up: %v", err)
	}

	for _, name := range []string{"proj-alpha", "proj-beta"} {
		info, ok := s.sandboxes[name]
		if !ok {
			t.Errorf("sandbox %s not created", name)
			continue
		}
		if info.Status != sandboxapi.SandboxInfoStatusRunning {
			t.Errorf("%s status = %q, want running", name, info.Status)
		}
	}
}

func TestUpIdempotent(t *testing.T) {
	s := newFakeState()
	o, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}
	o.PollTimeout = 5 * time.Second

	project := &config.Project{
		Name: "proj",
		Agents: map[string]*config.Agent{
			"worker": {Agent: "claude", Ports: []string{"8080:8080"}},
		},
	}

	// First Up.
	if err := o.Up(context.Background(), project); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if s.createCalls["proj-worker"] != 1 {
		t.Errorf("after first up: create calls = %d, want 1", s.createCalls["proj-worker"])
	}
	if len(s.publishedPorts["proj-worker"]) != 1 {
		t.Errorf("after first up: published ports = %d, want 1", len(s.publishedPorts["proj-worker"]))
	}

	// Second Up — must recreate nothing.
	if err := o.Up(context.Background(), project); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if s.createCalls["proj-worker"] != 1 {
		t.Errorf("after second up: create calls = %d, want still 1", s.createCalls["proj-worker"])
	}
	if len(s.publishedPorts["proj-worker"]) != 1 {
		t.Errorf("after second up: published ports = %d, want still 1", len(s.publishedPorts["proj-worker"]))
	}
}

func TestUpPortPublishIdempotent(t *testing.T) {
	s := newFakeState()
	o, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}
	o.PollTimeout = 5 * time.Second

	project := &config.Project{
		Name: "proj",
		Agents: map[string]*config.Agent{
			"web": {Agent: "claude", Ports: []string{"3000:3000", "3001:3001/udp"}},
		},
	}

	if err := o.Up(context.Background(), project); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if n := len(s.publishedPorts["proj-web"]); n != 2 {
		t.Errorf("published %d ports, want 2", n)
	}
	// Second Up — no extra ports.
	if err := o.Up(context.Background(), project); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if n := len(s.publishedPorts["proj-web"]); n != 2 {
		t.Errorf("after second up: %d ports, want still 2", n)
	}
}

func TestUpWaitRunningTimeout(t *testing.T) {
	s := newFakeState()
	s.neverRun = true
	o, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}
	// PollTimeout defaults to 50ms from newTestOrchestrator — fine for this test.

	project := minimalProject("proj", "slow", "claude")
	err = o.Up(context.Background(), project)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected 'timed out' in error, got: %v", err)
	}
}

func TestUpEmptyProjectName(t *testing.T) {
	s := newFakeState()
	_, err := NewOrchestrator(&fakeClient{s}, &fakeSbx{s}, "", io.Discard)
	if err == nil {
		t.Fatal("expected error for empty project name")
	}
}

func TestUpContextCancellation(t *testing.T) {
	s := newFakeState()
	s.neverRun = true
	o, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}
	o.PollTimeout = 10 * time.Second // long — we'll cancel before it

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	project := minimalProject("proj", "x", "claude")
	err = o.Up(ctx, project)
	if err == nil {
		t.Fatal("expected error after context cancel")
	}
}
