package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/glours/hakoniwa/internal/config"
)

// Graph is a resolved dependency graph over the agents in a project.
// It provides topological ordering and cycle detection beyond the static
// validation layer.
type Graph struct {
	// order is the topological ordering of agent names (dependencies first).
	order []string
	// edges maps agent name -> set of agent names it directly depends on.
	edges map[string][]string
}

// CycleError is returned by BuildGraph when the depends_on graph contains a cycle.
type CycleError struct {
	// Cycle is the offending cycle path, e.g. ["a", "b", "c", "a"].
	Cycle []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("dependency cycle detected: %s", strings.Join(e.Cycle, " -> "))
}

// BuildGraph constructs a dependency graph from the resolved agents in a
// project and returns it with a computed topological ordering.
//
// Epic-1 conditions (created, running, completed) and on_event all contribute
// to ordering — the distinction between condition types affects the orchestrator
// loop, not the graph topology.
//
// Declaration order within the YAML is used as a tiebreaker: agents are sorted
// by name to give a deterministic, stable topological order.
//
// Returns CycleError if the depends_on graph is cyclic.
func BuildGraph(agents map[string]*config.EffectiveAgent) (*Graph, error) {
	// Build adjacency: agent -> list of agents it depends on.
	edges := make(map[string][]string, len(agents))
	for name, ea := range agents {
		deps := make([]string, 0, len(ea.DependsOn))
		for dep := range ea.DependsOn {
			deps = append(deps, dep)
		}
		sort.Strings(deps)
		edges[name] = deps
	}

	// Kahn's algorithm for topological sort.
	// in-degree = number of agents that must come before this one.
	inDegree := make(map[string]int, len(agents))
	// reverse-edges: dep -> list of agents that depend on dep.
	revEdges := make(map[string][]string, len(agents))
	for name := range agents {
		if _, ok := inDegree[name]; !ok {
			inDegree[name] = 0
		}
		for _, dep := range edges[name] {
			inDegree[name]++
			revEdges[dep] = append(revEdges[dep], name)
		}
	}

	// Seed the queue with all agents that have no dependencies.
	// Sort for determinism.
	queue := make([]string, 0, len(agents))
	for name := range inDegree {
		if inDegree[name] == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)

	order := make([]string, 0, len(agents))
	for len(queue) > 0 {
		// Pop from front (stable, sorted).
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)

		// Reduce in-degree of dependents; enqueue those that reach zero.
		newlyReady := make([]string, 0)
		for _, dependent := range revEdges[n] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				newlyReady = append(newlyReady, dependent)
			}
		}
		sort.Strings(newlyReady)
		queue = append(queue, newlyReady...)
		sort.Strings(queue) // keep queue stable
	}

	// If any agent was not placed, a cycle exists.
	if len(order) != len(agents) {
		cycle := findCycle(edges)
		return nil, &CycleError{Cycle: cycle}
	}

	return &Graph{order: order, edges: edges}, nil
}

// Order returns the topological ordering of agent names, with dependencies
// (agents that must be created/started first) appearing earlier in the slice.
func (g *Graph) Order() []string {
	result := make([]string, len(g.order))
	copy(result, g.order)
	return result
}

// DependsOn returns the direct dependencies of the given agent.
func (g *Graph) DependsOn(name string) []string {
	deps, ok := g.edges[name]
	if !ok {
		return nil
	}
	result := make([]string, len(deps))
	copy(result, deps)
	return result
}

// findCycle finds and returns a cycle in the dependency graph using DFS.
// The edges map must contain only the subset of agents involved in the cycle.
func findCycle(edges map[string][]string) []string {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(edges))
	var stack []string

	var dfs func(name string) []string
	dfs = func(name string) []string {
		state[name] = visiting
		stack = append(stack, name)
		for _, dep := range edges[name] {
			switch state[dep] {
			case visiting:
				for i, n := range stack {
					if n == dep {
						cycle := make([]string, len(stack)-i+1)
						copy(cycle, stack[i:])
						cycle[len(cycle)-1] = dep // close the loop
						return cycle
					}
				}
				return []string{dep, dep}
			case unvisited:
				if cycle := dfs(dep); cycle != nil {
					return cycle
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = visited
		return nil
	}

	names := make([]string, 0, len(edges))
	for name := range edges {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if state[name] == unvisited {
			if cycle := dfs(name); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}
