package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glours/hakoniwa/internal/config"
)

// canonicalExample is the debug-collab YAML from the design doc.
const canonicalExample = `
name: bugfix-session

defaults:
  policy:
    default: balanced

channels: [ repro.ready, fix.ready ]

agents:
  reproducer:
    agent: claude
    template: node
    resources: { cpus: 4, memory: 8192 }
    ports: [ "8080:8080" ]
    secrets:
      - { value: "gh auth token", env: GH_TOKEN, host: api.github.com }
    emits: [ repro.ready ]

  fixer:
    agent: codex
    depends_on:
      reproducer: { condition: on_event, channel: repro.ready }
    subscribes: [ repro.ready ]
    reach: [ "reproducer:8080" ]
    policy:
      network: { allow: [ "*.github.com" ], deny: [ "*.telemetry.io" ] }
    emits: [ fix.ready ]

  test-writer:
    agent: gemini
    depends_on:
      fixer: { condition: on_event, channel: fix.ready }
    subscribes: [ fix.ready ]
    kits: [ ./kits/test-runner ]
`

func TestLoadCanonicalExample(t *testing.T) {
	result, err := loadString(canonicalExample)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := result.Project

	if p.Name != "bugfix-session" {
		t.Errorf("name = %q, want bugfix-session", p.Name)
	}
	if got := string(p.Defaults.Policy.Default); got != "balanced" {
		t.Errorf("defaults.policy.default = %q, want balanced", got)
	}
	if len(p.Channels) != 2 {
		t.Errorf("channels len = %d, want 2", len(p.Channels))
	}
	if len(p.Agents) != 3 {
		t.Errorf("agents count = %d, want 3", len(p.Agents))
	}

	// reproducer
	rep := p.Agents["reproducer"]
	if rep == nil {
		t.Fatal("agent reproducer missing")
	}
	if rep.Agent != "claude" {
		t.Errorf("reproducer.agent = %q, want claude", rep.Agent)
	}
	if rep.Resources.CPUs != 4 {
		t.Errorf("reproducer.resources.cpus = %v, want 4", rep.Resources.CPUs)
	}
	if rep.Resources.Memory != 8192 {
		t.Errorf("reproducer.resources.memory = %v, want 8192", rep.Resources.Memory)
	}
	if len(rep.Secrets) != 1 || rep.Secrets[0].Env != "GH_TOKEN" {
		t.Errorf("reproducer.secrets unexpected: %+v", rep.Secrets)
	}
	if len(rep.Emits) != 1 || rep.Emits[0] != "repro.ready" {
		t.Errorf("reproducer.emits = %v, want [repro.ready]", rep.Emits)
	}

	// fixer
	fix := p.Agents["fixer"]
	if fix == nil {
		t.Fatal("agent fixer missing")
	}
	dep, ok := fix.DependsOn["reproducer"]
	if !ok {
		t.Fatal("fixer.depends_on.reproducer missing")
	}
	if dep.Condition != config.ConditionOnEvent {
		t.Errorf("fixer depends_on condition = %q, want on_event", dep.Condition)
	}
	if dep.Channel != "repro.ready" {
		t.Errorf("fixer depends_on channel = %q, want repro.ready", dep.Channel)
	}
	if len(fix.Policy.Network.Allow) != 1 {
		t.Errorf("fixer policy.network.allow = %v, want [*.github.com]", fix.Policy.Network.Allow)
	}

	// test-writer
	tw := p.Agents["test-writer"]
	if tw == nil {
		t.Fatal("agent test-writer missing")
	}
	if len(tw.Kits) != 1 {
		t.Errorf("test-writer.kits = %v, want 1 kit", tw.Kits)
	}
}

func TestSourcePositions(t *testing.T) {
	result, err := loadString(canonicalExample)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Verify that positions are available for some key paths.
	for _, path := range []string{
		"name", "defaults", "channels", "agents",
		"agents.reproducer", "agents.fixer", "agents.fixer.policy",
	} {
		pos := result.PosFor(path)
		if pos.Line == 0 {
			t.Errorf("no source position for path %q", path)
		}
	}
}

func TestStrictDecodeUnknownKey(t *testing.T) {
	yaml := `
name: test
agents:
  a:
    agent: claude
    unknown_field: oops
`
	_, err := loadString(yaml)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "unknown_field") && !strings.Contains(err.Error(), "field") {
		t.Errorf("error %q does not mention the unknown field", err.Error())
	}
}

func TestLoadEmptyAgents(t *testing.T) {
	// A file with no agents field should still parse without crashing
	// (semantic validation catches the missing agents; that's task 1.4).
	yaml := "name: empty\n"
	result, err := loadString(yaml)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if result.Project.Name != "empty" {
		t.Errorf("name = %q, want \"empty\"", result.Project.Name)
	}
}

func TestFileResolutionOrder(t *testing.T) {
	dir := t.TempDir()
	// hakoniwa.yaml wins over all others
	writeFile(t, dir, "hako.yaml", "name: from-hako\nagents:\n  a:\n    agent: x\n")
	writeFile(t, dir, "hakoniwa.yaml", "name: from-hakoniwa\nagents:\n  a:\n    agent: x\n")

	found, err := config.FindProjectFile(dir)
	if err != nil {
		t.Fatalf("FindProjectFile: %v", err)
	}
	result, err := config.Load(found)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Project.Name != "from-hakoniwa" {
		t.Errorf("expected hakoniwa.yaml to win, got name=%q", result.Project.Name)
	}
}

func TestFileResolutionYmlExtension(t *testing.T) {
	// hakoniwa.yml is found before hako.yaml
	dir := t.TempDir()
	writeFile(t, dir, "hako.yaml", "name: from-hako\nagents:\n  a:\n    agent: x\n")
	writeFile(t, dir, "hakoniwa.yml", "name: from-hakoniwa-yml\nagents:\n  a:\n    agent: x\n")

	found, err := config.FindProjectFile(dir)
	if err != nil {
		t.Fatalf("FindProjectFile: %v", err)
	}
	result, err := config.Load(found)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Project.Name != "from-hakoniwa-yml" {
		t.Errorf("expected hakoniwa.yml to win, got name=%q", result.Project.Name)
	}
}

func TestFileResolutionSbxenv(t *testing.T) {
	// .sbxenv is accepted as last-resort fallback
	dir := t.TempDir()
	writeFile(t, dir, ".sbxenv", "name: from-sbxenv\nagents:\n  a:\n    agent: x\n")

	found, err := config.FindProjectFile(dir)
	if err != nil {
		t.Fatalf("FindProjectFile: %v", err)
	}
	result, err := config.Load(found)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Project.Name != "from-sbxenv" {
		t.Errorf("expected .sbxenv to be loaded, got name=%q", result.Project.Name)
	}
}

func TestMultiDocumentYAMLRejected(t *testing.T) {
	yaml := "name: first\nagents:\n  a:\n    agent: x\n---\nname: second\n"
	_, err := loadString(yaml)
	if err == nil {
		t.Fatal("expected error for multi-document YAML, got nil")
	}
	if !strings.Contains(err.Error(), "multi-document") {
		t.Errorf("error %q does not mention multi-document", err.Error())
	}
}

func TestAgentPolicyDefaultRejected(t *testing.T) {
	// policy.default must not be accepted at the per-agent level (project-only).
	yaml := `
name: test
agents:
  a:
    agent: claude
    policy:
      default: allow-all
`
	_, err := loadString(yaml)
	if err == nil {
		t.Fatal("expected error for per-agent policy.default, got nil")
	}
}

// --- helpers ---

func loadString(yaml string) (*config.LoadResult, error) {
	return config.LoadReader("test.yaml", strings.NewReader(yaml))
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
}
