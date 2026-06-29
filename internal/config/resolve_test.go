package config_test

import (
	"testing"

	"github.com/glours/hakoniwa/internal/config"
)

func TestResolveAgentsKitsUnion(t *testing.T) {
	p := &config.Project{
		Name: "test",
		Defaults: config.Defaults{
			Kits: []string{"oci://base:1.0", "shared-kit"},
		},
		Agents: map[string]*config.Agent{
			"worker": {
				Agent: "claude",
				Kits:  []string{"shared-kit", "extra-kit"}, // shared-kit is a dup
			},
		},
	}
	agents := config.ResolveAgents(p)
	w := agents["worker"]
	if w == nil {
		t.Fatal("worker missing")
	}
	// Expected: oci://base:1.0, shared-kit, extra-kit (no dup)
	want := []string{"oci://base:1.0", "shared-kit", "extra-kit"}
	if len(w.Kits) != len(want) {
		t.Fatalf("kits = %v, want %v", w.Kits, want)
	}
	for i, k := range want {
		if w.Kits[i] != k {
			t.Errorf("kits[%d] = %q, want %q", i, w.Kits[i], k)
		}
	}
}

func TestResolveAgentsSecretsNotInherited(t *testing.T) {
	p := &config.Project{
		Name: "test",
		Defaults: config.Defaults{
			Secrets: []config.Secret{
				{Env: "GLOBAL_TOKEN", Value: "echo global"},
			},
		},
		Agents: map[string]*config.Agent{
			"agent-a": {
				Agent:   "codex",
				Secrets: []config.Secret{{Env: "MY_TOKEN", Value: "echo mine"}},
			},
			"agent-b": {
				Agent: "gemini",
				// no secrets — should not inherit GLOBAL_TOKEN
			},
		},
	}
	agents := config.ResolveAgents(p)

	// agent-a has its own secret and does NOT implicitly get the project one.
	a := agents["agent-a"]
	if len(a.Secrets) != 1 || a.Secrets[0].Env != "MY_TOKEN" {
		t.Errorf("agent-a secrets = %+v, want only MY_TOKEN", a.Secrets)
	}

	// agent-b has no secrets.
	b := agents["agent-b"]
	if len(b.Secrets) != 0 {
		t.Errorf("agent-b should have no secrets, got %+v", b.Secrets)
	}
}

func TestResolveAgentsResourcesInherited(t *testing.T) {
	p := &config.Project{
		Name: "test",
		Defaults: config.Defaults{
			Resources: config.Resources{CPUs: 2, Memory: 4096},
		},
		Agents: map[string]*config.Agent{
			"full":    {Agent: "claude", Resources: config.Resources{CPUs: 8, Memory: 16384}},
			"partial": {Agent: "claude", Resources: config.Resources{CPUs: 4}},
			"empty":   {Agent: "claude"},
		},
	}
	agents := config.ResolveAgents(p)

	full := agents["full"]
	if full.Resources.CPUs != 8 || full.Resources.Memory != 16384 {
		t.Errorf("full resources = %+v, want 8/16384", full.Resources)
	}

	partial := agents["partial"]
	if partial.Resources.CPUs != 4 || partial.Resources.Memory != 4096 {
		t.Errorf("partial resources = %+v, want 4 cpus + inherited 4096 mem", partial.Resources)
	}

	empty := agents["empty"]
	if empty.Resources.CPUs != 2 || empty.Resources.Memory != 4096 {
		t.Errorf("empty resources = %+v, want inherited 2/4096", empty.Resources)
	}
}

func TestResolveAgentsTemplateInherited(t *testing.T) {
	p := &config.Project{
		Name:     "test",
		Defaults: config.Defaults{Template: "base-image"},
		Agents: map[string]*config.Agent{
			"override": {Agent: "claude", Template: "custom-image"},
			"inherit":  {Agent: "claude"},
		},
	}
	agents := config.ResolveAgents(p)

	if agents["override"].Template != "custom-image" {
		t.Errorf("override template = %q, want custom-image", agents["override"].Template)
	}
	if agents["inherit"].Template != "base-image" {
		t.Errorf("inherit template = %q, want base-image", agents["inherit"].Template)
	}
}

func TestResolveAgentsProjectPolicyCopied(t *testing.T) {
	p := &config.Project{
		Name: "test",
		Defaults: config.Defaults{
			Policy: config.ProjectPolicy{
				Default:       config.PolicyDefaultDenyAll,
				AllowWidening: false,
			},
		},
		Agents: map[string]*config.Agent{
			"a": {Agent: "claude"},
		},
	}
	agents := config.ResolveAgents(p)
	ea := agents["a"]
	if ea.ProjectPolicyDefault != config.PolicyDefaultDenyAll {
		t.Errorf("ProjectPolicyDefault = %q, want deny-all", ea.ProjectPolicyDefault)
	}
	if ea.AllowWidening != false {
		t.Error("AllowWidening should be false")
	}
}

func TestResolveAgentsCommandAlias(t *testing.T) {
	p := &config.Project{
		Name: "test",
		Agents: map[string]*config.Agent{
			"a": {Command: "claude"}, // uses command alias
		},
	}
	agents := config.ResolveAgents(p)
	if agents["a"].AgentKind != "claude" {
		t.Errorf("AgentKind = %q, want claude (from command alias)", agents["a"].AgentKind)
	}
}
