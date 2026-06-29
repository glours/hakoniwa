package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Severity indicates how serious a diagnostic is.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is a single validation finding with a source location and
// optional remediation hint.
type Diagnostic struct {
	Severity    Severity
	Path        string // dotted YAML path, e.g. "agents.fixer.depends_on.reproducer"
	Pos         SourcePos
	Message     string
	Remediation string // optional hint shown after the error message
}

func (d Diagnostic) Error() string {
	pos := d.Pos.String()
	if pos == "" {
		return fmt.Sprintf("%s: %s: %s", string(d.Severity), d.Path, d.Message)
	}
	return fmt.Sprintf("%s: %s: %s", pos, d.Path, d.Message)
}

// ValidationError is returned by Validate when there are one or more errors.
type ValidationError struct {
	Diagnostics []Diagnostic
}

func (e *ValidationError) Error() string {
	errs := 0
	warns := 0
	for _, d := range e.Diagnostics {
		if d.Severity == SeverityError {
			errs++
		} else {
			warns++
		}
	}
	var msgs []string
	for _, d := range e.Diagnostics {
		msgs = append(msgs, d.Error())
	}
	summary := fmt.Sprintf("validation failed: %d error(s), %d warning(s)", errs, warns)
	return strings.Join(append(msgs, summary), "\n")
}

// validPolicyDefaults is the set of allowed values for policy.default.
var validPolicyDefaults = map[PolicyDefault]bool{
	PolicyDefaultAllowAll: true,
	PolicyDefaultBalanced: true,
	PolicyDefaultDenyAll:  true,
}

// validConditions is the set of allowed depends_on conditions.
var validConditions = map[DependsOnCondition]bool{
	ConditionCreated:   true,
	ConditionRunning:   true,
	ConditionCompleted: true,
	ConditionOnEvent:   true,
}

// Validate runs layer-1 static validation on a loaded project file.
// It is a pure function of the file — no system access is needed.
//
// Checks performed (Epic 1 scope; channel/on_event/reach checks deferred to Epic 2):
//   - Required fields: non-empty agents map; each agent has agent/command set
//   - Enum: policy.default value
//   - Enum: depends_on condition values
//   - Port spec: host port required in every element of ports[]
//   - depends_on target existence (every key references a defined agent)
//   - DAG acyclicity (on depends_on edges)
//
// All errors are collected before returning; the caller sees the full list.
func Validate(r *LoadResult) *ValidationError {
	var diags []Diagnostic
	p := r.Project

	add := func(sev Severity, path, msg, remediation string) {
		diags = append(diags, Diagnostic{
			Severity:    sev,
			Path:        path,
			Pos:         r.PosFor(path),
			Message:     msg,
			Remediation: remediation,
		})
	}
	errorf := func(path, msg string) { add(SeverityError, path, msg, "") }

	// ── Required: non-empty agents ──────────────────────────────────────────
	if len(p.Agents) == 0 {
		errorf("agents", "no agents defined; at least one agent is required")
	}

	// ── Per-agent checks ────────────────────────────────────────────────────
	for name, agent := range p.Agents {
		base := "agents." + name

		// Required: agent/command
		if agent.Agent == "" && agent.Command == "" {
			errorf(base, "agent must have an 'agent' (or 'command') field set")
		}

		// Enum: depends_on conditions
		for depName, dep := range agent.DependsOn {
			path := base + ".depends_on." + depName
			if !validConditions[dep.Condition] {
				errorf(path, fmt.Sprintf("unknown condition %q (valid: created, running, completed, on_event)",
					dep.Condition))
			}
			// depends_on target existence
			if _, exists := p.Agents[depName]; !exists {
				errorf(path, fmt.Sprintf("depends_on references undefined agent %q", depName))
			}
		}

		// Port specs
		for i, portSpec := range agent.Ports {
			path := fmt.Sprintf("%s.ports[%d]", base, i)
			if err := validatePortSpec(portSpec); err != nil {
				errorf(path, err.Error())
			}
		}
	}

	// ── Enum: defaults.policy.default ───────────────────────────────────────
	if p.Defaults.Policy.Default != "" && !validPolicyDefaults[p.Defaults.Policy.Default] {
		errorf("defaults.policy.default",
			fmt.Sprintf("unknown policy default %q (valid: allow-all, balanced, deny-all)",
				p.Defaults.Policy.Default))
	}

	// ── DAG acyclicity ───────────────────────────────────────────────────────
	if cycle := detectCycle(p.Agents); cycle != nil {
		errorf("agents", fmt.Sprintf("dependency cycle detected: %s", strings.Join(cycle, " → ")))
	}

	if len(diags) == 0 {
		return nil
	}
	return &ValidationError{Diagnostics: diags}
}

// validatePortSpec checks that a port spec is well-formed and that the host
// port is explicitly given. Accepted formats:
//
//	HOST_PORT:SANDBOX_PORT[/PROTO]
//	HOST_IP:HOST_PORT:SANDBOX_PORT[/PROTO]
func validatePortSpec(spec string) error {
	// Strip optional protocol suffix.
	proto := ""
	if idx := strings.LastIndex(spec, "/"); idx >= 0 {
		proto = spec[idx+1:]
		spec = spec[:idx]
		if proto != "tcp" && proto != "udp" && proto != "sctp" {
			return fmt.Errorf("unknown protocol %q in port spec (valid: tcp, udp, sctp)", proto)
		}
	}

	parts := strings.Split(spec, ":")
	var hostPort, sandboxPort string
	switch len(parts) {
	case 2: // HOST_PORT:SANDBOX_PORT
		hostPort = parts[0]
		sandboxPort = parts[1]
	case 3: // HOST_IP:HOST_PORT:SANDBOX_PORT
		hostPort = parts[1]
		sandboxPort = parts[2]
	default:
		return fmt.Errorf("invalid port spec %q: expected HOST_PORT:SANDBOX_PORT or HOST_IP:HOST_PORT:SANDBOX_PORT", spec)
	}

	if hostPort == "" {
		return fmt.Errorf("host port is required in port spec %q", spec)
	}
	if n, err := strconv.Atoi(hostPort); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid host port %q in port spec (must be 1–65535)", hostPort)
	}
	if sandboxPort == "" {
		return fmt.Errorf("sandbox port is required in port spec %q", spec)
	}
	if n, err := strconv.Atoi(sandboxPort); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid sandbox port %q in port spec (must be 1–65535)", sandboxPort)
	}
	return nil
}

// detectCycle uses DFS to find a cycle in the depends_on graph.
// Returns the cycle path as a slice of agent names, or nil if acyclic.
func detectCycle(agents map[string]*Agent) []string {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(agents))
	var stack []string

	var dfs func(name string) []string
	dfs = func(name string) []string {
		state[name] = visiting
		stack = append(stack, name)
		agent, ok := agents[name]
		if ok {
			for dep := range agent.DependsOn {
				switch state[dep] {
				case visiting:
					// Found a cycle — extract it from the stack.
					for i, n := range stack {
						if n == dep {
							cycle := make([]string, len(stack)-i+1)
							copy(cycle, stack[i:])
							cycle[len(cycle)-1] = dep // close the loop
							return cycle
						}
					}
					return []string{dep, dep} // fallback
				case unvisited:
					if cycle := dfs(dep); cycle != nil {
						return cycle
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = visited
		return nil
	}

	for name := range agents {
		if state[name] == unvisited {
			if cycle := dfs(name); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}
