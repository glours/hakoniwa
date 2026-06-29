package orchestrator

import (
	"context"
	"testing"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

func TestApplyReachPublishesAndInjectsEnv(t *testing.T) {
	s := newFakeState()
	s.sandboxes["proj-server"] = &sandbox.SandboxInfo{
		Name:   "proj-server",
		Status: sandboxapi.SandboxInfoStatusRunning,
	}
	orch, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}

	ea := &config.EffectiveAgent{
		Reach: []string{"server:8080"},
	}
	agents := map[string]*config.EffectiveAgent{
		"server": {Ports: []string{"8080:8080"}},
	}

	env, err := orch.ApplyReach(context.Background(), "client", ea, agents)
	if err != nil {
		t.Fatalf("ApplyReach: %v", err)
	}

	key := "HAKO_REACH_SERVER_8080"
	val, ok := env[key]
	if !ok {
		t.Fatalf("env var %q not set; env=%v", key, env)
	}
	if val == "" {
		t.Errorf("env var %q is empty", key)
	}
	// Value must start with the reach hostname and include a colon+port.
	if val[:len(reachHostname)] != reachHostname {
		t.Errorf("env var %q = %q, expected host prefix %q", key, val, reachHostname)
	}
}

func TestApplyReachAlreadyPublished(t *testing.T) {
	s := newFakeState()
	s.sandboxes["proj-server"] = &sandbox.SandboxInfo{
		Name:   "proj-server",
		Status: sandboxapi.SandboxInfoStatusRunning,
	}
	proto := sandboxapi.PublishedPortProtocol("tcp")
	s.publishedPorts["proj-server"] = []sandbox.PublishedPort{
		{HostPort: 9000, SandboxPort: 8080, Protocol: proto, HostIp: "127.0.0.1"},
	}

	orch, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}

	ea := &config.EffectiveAgent{Reach: []string{"server:8080"}}
	env, err := orch.ApplyReach(context.Background(), "client", ea, nil)
	if err != nil {
		t.Fatalf("ApplyReach (already published): %v", err)
	}
	// Host port must be the existing binding (9000).
	val := env["HAKO_REACH_SERVER_8080"]
	expected := reachHostname + ":9000"
	if val != expected {
		t.Errorf("env = %q, want %q", val, expected)
	}
}

func TestApplyReachEmpty(t *testing.T) {
	orch, _ := newTestOrchestrator(newFakeState(), "proj")
	ea := &config.EffectiveAgent{}
	env, err := orch.ApplyReach(context.Background(), "client", ea, nil)
	if err != nil {
		t.Fatalf("ApplyReach empty: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("expected empty env, got %v", env)
	}
}

func TestNormaliseEnvSegment(t *testing.T) {
	cases := []struct{ in, want string }{
		{"server", "SERVER"},
		{"my-agent", "MY_AGENT"},
		{"8080", "8080"},
		{"some.agent", "SOME_AGENT"},
	}
	for _, tc := range cases {
		got := normaliseEnvSegment(tc.in)
		if got != tc.want {
			t.Errorf("normaliseEnvSegment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
