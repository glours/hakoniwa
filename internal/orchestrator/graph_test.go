package orchestrator_test

import (
	"testing"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/orchestrator"
)

func makeAgents(specs map[string][]string) map[string]*config.EffectiveAgent {
	agents := make(map[string]*config.EffectiveAgent, len(specs))
	for name, deps := range specs {
		ea := &config.EffectiveAgent{Name: name, AgentKind: "claude"}
		if len(deps) > 0 {
			ea.DependsOn = make(map[string]config.DependsOnEntry, len(deps))
			for _, dep := range deps {
				ea.DependsOn[dep] = config.DependsOnEntry{Condition: config.ConditionRunning}
			}
		}
		agents[name] = ea
	}
	return agents
}

func TestBuildGraphEmptyAgents(t *testing.T) {
	g, err := orchestrator.BuildGraph(map[string]*config.EffectiveAgent{})
	if err != nil {
		t.Fatalf("BuildGraph(empty): %v", err)
	}
	if order := g.Order(); len(order) != 0 {
		t.Errorf("expected empty order, got %v", order)
	}
}

func TestBuildGraphDanglingDep(t *testing.T) {
	agents := makeAgents(map[string][]string{
		"a": {"nonexistent"},
	})
	_, err := orchestrator.BuildGraph(agents)
	if err == nil {
		t.Fatal("expected error for dangling dep, got nil")
	}
	unknown, ok := err.(*orchestrator.UnknownDepError)
	if !ok {
		t.Fatalf("expected *UnknownDepError, got %T: %v", err, err)
	}
	if unknown.Dep != "nonexistent" {
		t.Errorf("UnknownDepError.Dep = %q, want nonexistent", unknown.Dep)
	}
}

// a <- b <- c  (a must start first)
func TestBuildGraphLinearChain(t *testing.T) {
	agents := makeAgents(map[string][]string{
		"a": nil,
		"b": {"a"},
		"c": {"b"},
	})
	g, err := orchestrator.BuildGraph(agents)
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	order := g.Order()
	if len(order) != 3 {
		t.Fatalf("order len = %d, want 3", len(order))
	}
	// a must come before b, b before c
	pos := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}
	if pos("a") >= pos("b") {
		t.Errorf("a must come before b in order %v", order)
	}
	if pos("b") >= pos("c") {
		t.Errorf("b must come before c in order %v", order)
	}
}

func TestBuildGraphParallelBranches(t *testing.T) {
	// root <- left, root <- right, left+right <- join
	agents := makeAgents(map[string][]string{
		"root":  nil,
		"left":  {"root"},
		"right": {"root"},
		"join":  {"left", "right"},
	})
	g, err := orchestrator.BuildGraph(agents)
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	order := g.Order()
	pos := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}
	if pos("root") >= pos("left") {
		t.Errorf("root must precede left in %v", order)
	}
	if pos("root") >= pos("right") {
		t.Errorf("root must precede right in %v", order)
	}
	if pos("left") >= pos("join") {
		t.Errorf("left must precede join in %v", order)
	}
	if pos("right") >= pos("join") {
		t.Errorf("right must precede join in %v", order)
	}
}

func TestBuildGraphCycleDetected(t *testing.T) {
	// a -> b -> c -> a
	agents := makeAgents(map[string][]string{
		"a": {"c"},
		"b": {"a"},
		"c": {"b"},
	})
	_, err := orchestrator.BuildGraph(agents)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	cycleErr, ok := err.(*orchestrator.CycleError)
	if !ok {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
	if len(cycleErr.Cycle) < 2 {
		t.Errorf("cycle path is too short: %v", cycleErr.Cycle)
	}
	// The path must close on itself.
	n := len(cycleErr.Cycle)
	if cycleErr.Cycle[0] != cycleErr.Cycle[n-1] {
		t.Errorf("cycle path does not close: %v", cycleErr.Cycle)
	}
}

func TestBuildGraphSelfLoop(t *testing.T) {
	agents := makeAgents(map[string][]string{
		"a": {"a"},
	})
	_, err := orchestrator.BuildGraph(agents)
	if err == nil {
		t.Fatal("expected cycle error for self-loop, got nil")
	}
}

func TestBuildGraphNoDependencies(t *testing.T) {
	agents := makeAgents(map[string][]string{
		"x": nil,
		"y": nil,
		"z": nil,
	})
	g, err := orchestrator.BuildGraph(agents)
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	order := g.Order()
	if len(order) != 3 {
		t.Fatalf("order len = %d, want 3", len(order))
	}
}

func TestBuildGraphOrderDeterministic(t *testing.T) {
	// Running BuildGraph twice on the same input should produce the same order.
	agents := makeAgents(map[string][]string{
		"alpha": nil,
		"beta":  {"alpha"},
		"gamma": {"alpha"},
		"delta": {"beta", "gamma"},
	})
	g1, _ := orchestrator.BuildGraph(agents)
	g2, _ := orchestrator.BuildGraph(agents)
	o1 := g1.Order()
	o2 := g2.Order()
	if len(o1) != len(o2) {
		t.Fatalf("non-deterministic order lengths: %v vs %v", o1, o2)
	}
	for i := range o1 {
		if o1[i] != o2[i] {
			t.Errorf("non-deterministic order at [%d]: %v vs %v", i, o1, o2)
		}
	}
}

func TestGraphDependsOn(t *testing.T) {
	agents := makeAgents(map[string][]string{
		"a": nil,
		"b": {"a"},
	})
	g, _ := orchestrator.BuildGraph(agents)
	deps := g.DependsOn("b")
	if len(deps) != 1 || deps[0] != "a" {
		t.Errorf("DependsOn(b) = %v, want [a]", deps)
	}
	if len(g.DependsOn("a")) != 0 {
		t.Errorf("DependsOn(a) should be empty, got %v", g.DependsOn("a"))
	}
	if len(g.DependsOn("unknown")) != 0 {
		t.Errorf("DependsOn(unknown) should be empty, got %v", g.DependsOn("unknown"))
	}
}
