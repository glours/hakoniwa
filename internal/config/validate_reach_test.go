package config_test

import (
	"strings"
	"testing"

	"github.com/glours/hakoniwa/internal/config"
)

// ---------------------------------------------------------------------------
// Reach layer-1 validation tests
// ---------------------------------------------------------------------------

func TestValidateReachValid(t *testing.T) {
	lr := &config.LoadResult{
		Project: &config.Project{
			Name: "proj",
			Agents: map[string]*config.Agent{
				"server": {Agent: "shell", Ports: []string{"8080:8080"}},
				"client": {Agent: "shell", Reach: []string{"server:8080"}},
			},
		},
		Filename:  "test.yaml",
		Positions: make(map[string]config.SourcePos),
	}
	if err := config.Validate(lr); err != nil {
		t.Errorf("valid reach should pass: %v", err)
	}
}

func TestValidateReachUndeclaredAgent(t *testing.T) {
	lr := &config.LoadResult{
		Project: &config.Project{
			Name: "proj",
			Agents: map[string]*config.Agent{
				"client": {Agent: "shell", Reach: []string{"ghost:8080"}},
			},
		},
		Filename:  "test.yaml",
		Positions: make(map[string]config.SourcePos),
	}
	ve := config.Validate(lr)
	if ve == nil {
		t.Fatal("expected error for reach to undefined agent")
	}
	if !strings.Contains(ve.Error(), "ghost") {
		t.Errorf("expected 'ghost' in error: %v", ve)
	}
}

func TestValidateReachUnpublishedPort(t *testing.T) {
	lr := &config.LoadResult{
		Project: &config.Project{
			Name: "proj",
			Agents: map[string]*config.Agent{
				"server": {Agent: "shell", Ports: []string{"9000:9000"}},
				"client": {Agent: "shell", Reach: []string{"server:8080"}}, // 8080 not published
			},
		},
		Filename:  "test.yaml",
		Positions: make(map[string]config.SourcePos),
	}
	ve := config.Validate(lr)
	if ve == nil {
		t.Fatal("expected error for reach to unpublished port")
	}
	if !strings.Contains(ve.Error(), "8080") {
		t.Errorf("expected '8080' in error: %v", ve)
	}
}

func TestValidateReachMalformed(t *testing.T) {
	lr := &config.LoadResult{
		Project: &config.Project{
			Name: "proj",
			Agents: map[string]*config.Agent{
				"client": {Agent: "shell", Reach: []string{"nocolon"}},
			},
		},
		Filename:  "test.yaml",
		Positions: make(map[string]config.SourcePos),
	}
	ve := config.Validate(lr)
	if ve == nil {
		t.Fatal("expected error for malformed reach entry")
	}
}

func TestValidateReachWithProtoInPorts(t *testing.T) {
	// Port declared with /tcp suffix — the validator should still accept a reach to the port number.
	lr := &config.LoadResult{
		Project: &config.Project{
			Name: "proj",
			Agents: map[string]*config.Agent{
				"server": {Agent: "shell", Ports: []string{"8080:8080/tcp"}},
				"client": {Agent: "shell", Reach: []string{"server:8080"}},
			},
		},
		Filename:  "test.yaml",
		Positions: make(map[string]config.SourcePos),
	}
	if err := config.Validate(lr); err != nil {
		t.Errorf("reach with /tcp port suffix should pass: %v", err)
	}
}
