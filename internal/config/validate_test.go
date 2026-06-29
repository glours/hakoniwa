package config_test

import (
	"strings"
	"testing"

	"github.com/glours/hakoniwa/internal/config"
)

// makeProject is a helper to build a minimal valid project for testing.
func makeProject(agents map[string]*config.Agent) *config.Project {
	return &config.Project{
		Name:   "test",
		Agents: agents,
	}
}

func validate(p *config.Project) *config.ValidationError {
	r := &config.LoadResult{
		Project:   p,
		Filename:  "test.yaml",
		Positions: make(map[string]config.SourcePos),
	}
	return config.Validate(r)
}

func assertNoError(t *testing.T, err *config.ValidationError) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected validation error:\n%s", err.Error())
	}
}

func assertHasError(t *testing.T, err *config.ValidationError, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("error does not contain %q:\n%s", substr, err.Error())
	}
}

func TestValidateMinimalValid(t *testing.T) {
	p := makeProject(map[string]*config.Agent{
		"a": {Agent: "claude"},
	})
	assertNoError(t, validate(p))
}

func TestValidateNoAgents(t *testing.T) {
	p := makeProject(map[string]*config.Agent{})
	assertHasError(t, validate(p), "no agents defined")
}

func TestValidateAgentMissingAgentField(t *testing.T) {
	p := makeProject(map[string]*config.Agent{
		"a": {Template: "node"}, // no agent/command
	})
	assertHasError(t, validate(p), "'agent' (or 'command') field")
}

func TestValidatePolicyDefaultBadEnum(t *testing.T) {
	p := &config.Project{
		Name: "test",
		Defaults: config.Defaults{
			Policy: config.ProjectPolicy{Default: "banana"},
		},
		Agents: map[string]*config.Agent{"a": {Agent: "claude"}},
	}
	assertHasError(t, validate(p), "unknown policy default")
}

func TestValidatePolicyDefaultAllValid(t *testing.T) {
	for _, v := range []config.PolicyDefault{
		config.PolicyDefaultAllowAll,
		config.PolicyDefaultBalanced,
		config.PolicyDefaultDenyAll,
	} {
		p := &config.Project{
			Name:     "test",
			Defaults: config.Defaults{Policy: config.ProjectPolicy{Default: v}},
			Agents:   map[string]*config.Agent{"a": {Agent: "claude"}},
		}
		assertNoError(t, validate(p))
	}
}

func TestValidateDependsOnUnknownTarget(t *testing.T) {
	p := makeProject(map[string]*config.Agent{
		"fixer": {
			Agent: "codex",
			DependsOn: map[string]config.DependsOnEntry{
				"nonexistent": {Condition: config.ConditionRunning},
			},
		},
	})
	assertHasError(t, validate(p), `undefined agent "nonexistent"`)
}

func TestValidateDependsOnBadCondition(t *testing.T) {
	p := makeProject(map[string]*config.Agent{
		"a": {Agent: "claude"},
		"b": {
			Agent: "codex",
			DependsOn: map[string]config.DependsOnEntry{
				"a": {Condition: "typo"},
			},
		},
	})
	assertHasError(t, validate(p), `unknown condition "typo"`)
}

func TestValidateMultipleErrors(t *testing.T) {
	// bad enum + unknown depends_on target → both should be reported
	p := &config.Project{
		Name: "test",
		Defaults: config.Defaults{
			Policy: config.ProjectPolicy{Default: "bad-value"},
		},
		Agents: map[string]*config.Agent{
			"fixer": {
				Agent: "codex",
				DependsOn: map[string]config.DependsOnEntry{
					"missing": {Condition: config.ConditionRunning},
				},
			},
		},
	}
	err := validate(p)
	if err == nil {
		t.Fatal("expected validation errors, got nil")
	}
	errCount := 0
	for _, d := range err.Diagnostics {
		if d.Severity == config.SeverityError {
			errCount++
		}
	}
	if errCount < 2 {
		t.Errorf("expected at least 2 errors, got %d:\n%s", errCount, err.Error())
	}
	// Both specific messages should appear.
	assertHasError(t, err, "unknown policy default")
	assertHasError(t, err, "undefined agent")
}

func TestValidatePortSpecValid(t *testing.T) {
	cases := []string{
		"8080:8080",
		"127.0.0.1:8080:8080",
		"443:443/tcp",
		"5353:5353/udp",
	}
	for _, spec := range cases {
		p := makeProject(map[string]*config.Agent{
			"a": {Agent: "claude", Ports: []string{spec}},
		})
		assertNoError(t, validate(p))
	}
}

func TestValidatePortSpecInvalid(t *testing.T) {
	cases := []struct {
		spec    string
		wantMsg string
	}{
		{"8080", "invalid port spec"},
		{"abc:8080", "invalid host port"},
		{"8080:", "sandbox port is required"},
		{"0:8080", "invalid host port"},
		{"8080:99999", "invalid sandbox port"},
		{"8080:8080/ftp", "unknown protocol"},
		{"a:b:c:d", "invalid port spec"},
	}
	for _, tc := range cases {
		p := makeProject(map[string]*config.Agent{
			"a": {Agent: "claude", Ports: []string{tc.spec}},
		})
		assertHasError(t, validate(p), tc.wantMsg)
	}
}

func TestValidateDependsOnAllConditions(t *testing.T) {
	// All four valid conditions should pass validation.
	for _, cond := range []config.DependsOnCondition{
		config.ConditionCreated,
		config.ConditionRunning,
		config.ConditionCompleted,
		config.ConditionOnEvent,
	} {
		p := makeProject(map[string]*config.Agent{
			"a": {Agent: "claude"},
			"b": {
				Agent: "codex",
				DependsOn: map[string]config.DependsOnEntry{
					"a": {Condition: cond, Channel: "ch.a"},
				},
			},
		})
		if err := validate(p); err != nil {
			t.Errorf("condition %q should pass validation, got: %v", cond, err)
		}
	}
}

func TestValidateSelfLoop(t *testing.T) {
	p := makeProject(map[string]*config.Agent{
		"a": {
			Agent: "claude",
			DependsOn: map[string]config.DependsOnEntry{
				"a": {Condition: config.ConditionRunning},
			},
		},
	})
	// Both a cycle error and an existence error are expected (a depends on itself,
	// and the self-dep also shows as an existing agent so no "undefined" error).
	err := validate(p)
	if err == nil {
		t.Fatal("expected cycle error for self-loop, got nil")
	}
	assertHasError(t, err, "cycle")
}


	func TestValidateDAGCycleDetected(t *testing.T) {
	// a -> b -> c -> a
	p := makeProject(map[string]*config.Agent{
		"a": {Agent: "claude", DependsOn: map[string]config.DependsOnEntry{
			"c": {Condition: config.ConditionRunning},
		}},
		"b": {Agent: "codex", DependsOn: map[string]config.DependsOnEntry{
			"a": {Condition: config.ConditionRunning},
		}},
		"c": {Agent: "gemini", DependsOn: map[string]config.DependsOnEntry{
			"b": {Condition: config.ConditionRunning},
		}},
	})
	err := validate(p)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	assertHasError(t, err, "cycle")
}

func TestValidateDAGNoCycle(t *testing.T) {
	// a ← b ← c (linear chain, no cycle)
	p := makeProject(map[string]*config.Agent{
		"a": {Agent: "claude"},
		"b": {Agent: "codex", DependsOn: map[string]config.DependsOnEntry{
			"a": {Condition: config.ConditionRunning},
		}},
		"c": {Agent: "gemini", DependsOn: map[string]config.DependsOnEntry{
			"b": {Condition: config.ConditionRunning},
		}},
	})
	assertNoError(t, validate(p))
}

func TestValidateSourcePositionsInDiagnostics(t *testing.T) {
	// Load a real file so positions are populated; introduce a bad enum.
	yaml := `
name: test
defaults:
  policy:
    default: bad-enum
agents:
  a:
    agent: claude
`
	r, err := config.LoadReader("test.yaml", strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	valErr := config.Validate(r)
	if valErr == nil {
		t.Fatal("expected validation error")
	}
	for _, d := range valErr.Diagnostics {
		if strings.Contains(d.Path, "policy.default") {
			if d.Pos.Line == 0 {
				t.Errorf("diagnostic for policy.default has zero line; positions not attached")
			}
			return
		}
	}
	t.Error("no diagnostic found for defaults.policy.default")
}
