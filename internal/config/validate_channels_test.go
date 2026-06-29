package config

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Channel name validation
// ---------------------------------------------------------------------------

func TestValidateChannelNameValid(t *testing.T) {
	cases := []string{"ready", "repro.ready", "fix.done", "a1", "repro.fix.done"}
	for _, ch := range cases {
		t.Run(ch, func(t *testing.T) {
			var errs []string
			validateChannels(&Project{Channels: []string{ch}}, func(_, msg string) { errs = append(errs, msg) })
			if len(errs) != 0 {
				t.Errorf("valid name %q rejected: %v", ch, errs)
			}
		})
	}
}

func TestValidateChannelNameInvalid(t *testing.T) {
	cases := []string{"", "UPPER", "has space", "dot.", ".start", "two..dots"}
	for _, ch := range cases {
		t.Run(ch, func(t *testing.T) {
			var errs []string
			validateChannels(&Project{Channels: []string{ch}}, func(_, msg string) { errs = append(errs, msg) })
			if len(errs) == 0 {
				t.Errorf("invalid name %q should be rejected", ch)
			}
		})
	}
}

func TestValidateDuplicateChannel(t *testing.T) {
	var errs []string
	validateChannels(&Project{Channels: []string{"ready", "ready"}},
		func(_, msg string) { errs = append(errs, msg) })
	if len(errs) == 0 {
		t.Fatal("expected error for duplicate channel")
	}
}

// ---------------------------------------------------------------------------
// Channel closed vocabulary — emits / subscribes / depends_on.channel
// ---------------------------------------------------------------------------

func mkProject(channels []string, agentsF func(map[string]*Agent)) *Project {
	p := &Project{
		Name:     "test",
		Channels: channels,
		Agents:   map[string]*Agent{"alpha": {Agent: "claude"}},
	}
	agentsF(p.Agents)
	return p
}

func TestValidateEmitsUndeclaredChannel(t *testing.T) {
	lr := &LoadResult{Project: mkProject([]string{"ready"}, func(m map[string]*Agent) {
		m["alpha"].Emits = []string{"notdeclared"}
	})}
	ve := Validate(lr)
	if ve == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(ve.Error(), "notdeclared") {
		t.Errorf("expected 'notdeclared' in error, got: %v", ve)
	}
}

func TestValidateEmitsDeclaredChannel(t *testing.T) {
	lr := &LoadResult{Project: mkProject([]string{"ready"}, func(m map[string]*Agent) {
		m["alpha"].Emits = []string{"ready"}
	})}
	if err := Validate(lr); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateSubscribesUndeclaredChannel(t *testing.T) {
	lr := &LoadResult{Project: mkProject([]string{"ready"}, func(m map[string]*Agent) {
		m["alpha"].Subscribes = []string{"missing"}
	})}
	ve := Validate(lr)
	if ve == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateDependsOnChannelUndeclared(t *testing.T) {
	lr := &LoadResult{Project: mkProject([]string{"ready"}, func(m map[string]*Agent) {
		m["beta"] = &Agent{Agent: "codex", DependsOn: map[string]DependsOnEntry{
			"alpha": {Condition: ConditionOnEvent, Channel: "nosuchchannel"},
		}}
	})}
	ve := Validate(lr)
	if ve == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(ve.Error(), "nosuchchannel") {
		t.Errorf("expected channel name in error: %v", ve)
	}
}

// ---------------------------------------------------------------------------
// on_event requires channel
// ---------------------------------------------------------------------------

func TestValidateOnEventRequiresChannel(t *testing.T) {
	lr := &LoadResult{Project: &Project{
		Name: "proj",
		Agents: map[string]*Agent{
			"producer": {Agent: "claude"},
			"consumer": {Agent: "codex", DependsOn: map[string]DependsOnEntry{
				"producer": {Condition: ConditionOnEvent}, // missing channel
			}},
		},
	}}
	ve := Validate(lr)
	if ve == nil {
		t.Fatal("expected error: on_event without channel")
	}
	if !strings.Contains(ve.Error(), "channel") {
		t.Errorf("expected 'channel' in error: %v", ve)
	}
}

func TestValidateOnEventWithChannel(t *testing.T) {
	lr := &LoadResult{Project: &Project{
		Name:     "proj",
		Channels: []string{"ready"},
		Agents: map[string]*Agent{
			"producer": {Agent: "claude", Emits: []string{"ready"}},
			"consumer": {Agent: "codex", DependsOn: map[string]DependsOnEntry{
				"producer": {Condition: ConditionOnEvent, Channel: "ready"},
			}, Subscribes: []string{"ready"}},
		},
	}}
	if err := Validate(lr); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Single-emitter rule
// ---------------------------------------------------------------------------

func TestValidateSingleEmitterViolation(t *testing.T) {
	lr := &LoadResult{Project: &Project{
		Name:     "proj",
		Channels: []string{"ready"},
		Agents: map[string]*Agent{
			"a": {Agent: "claude", Emits: []string{"ready"}},
			"b": {Agent: "codex", Emits: []string{"ready"}}, // duplicate emitter
		},
	}}
	ve := Validate(lr)
	if ve == nil {
		t.Fatal("expected error: two agents emit same channel")
	}
	if !strings.Contains(ve.Error(), "single emitter") {
		t.Errorf("expected 'single emitter' in error: %v", ve)
	}
}
