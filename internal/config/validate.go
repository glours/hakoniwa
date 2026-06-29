package config

import (
	"fmt"
	"regexp"
	"sort"
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

// Error formats the diagnostic as "pos: severity: path: message".
// If Pos.Line == 0 the position is shown as just the filename (or omitted if
// the filename is also empty).
func (d Diagnostic) Error() string {
	pos := d.Pos.String()
	if pos == "" {
		return fmt.Sprintf("%s: %s: %s", string(d.Severity), d.Path, d.Message)
	}
	return fmt.Sprintf("%s: %s: %s: %s", pos, string(d.Severity), d.Path, d.Message)
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
// Checks performed (Epic 1 scope + Epic 2 channel/on_event/reach additions):
//   - Required fields: non-empty agents map; each agent has agent/command set
//   - Enum: policy.default value
//   - Enum: depends_on condition values
//   - Port spec: valid HOST_PORT:SBX_PORT [or HOST_IP:HOST_PORT:SBX_PORT]
//     with optional /tcp|udp|sctp; host and sandbox port in 1-65535
//   - depends_on target existence (every key references a defined agent)
//   - DAG acyclicity (on depends_on edges)
//   - Channel names: must match ^[a-z0-9]+(\.[a-z0-9]+)*$
//   - Channel closed vocabulary: emits/subscribes/depends_on.channel must
//     reference a name declared in channels[]
//   - on_event condition requires a channel field
//   - Single emitter per channel (v0): at most one agent emits each channel
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

	// -- Required: non-empty agents --
	if len(p.Agents) == 0 {
		errorf("agents", "no agents defined; at least one agent is required")
	}

	// -- Per-agent checks (sorted for deterministic output) --
	agentNames := make([]string, 0, len(p.Agents))
	for name := range p.Agents {
		agentNames = append(agentNames, name)
	}
	sort.Strings(agentNames)

	for _, name := range agentNames {
		agent := p.Agents[name]
		base := "agents." + name

		// Required: agent/command
		if agent.Agent == "" && agent.Command == "" {
			errorf(base, "agent must have an 'agent' (or 'command') field set")
		}

		// Enum: depends_on conditions + target existence (sorted for determinism)
		depNames := make([]string, 0, len(agent.DependsOn))
		for depName := range agent.DependsOn {
			depNames = append(depNames, depName)
		}
		sort.Strings(depNames)
		for _, depName := range depNames {
			dep := agent.DependsOn[depName]
			path := base + ".depends_on." + depName
			if !validConditions[dep.Condition] {
				errorf(path, fmt.Sprintf("unknown condition %q (valid: created, running, completed, on_event)",
					dep.Condition))
			}
			// depends_on target existence
			if _, exists := p.Agents[depName]; !exists {
				errorf(path, fmt.Sprintf("depends_on references undefined agent %q", depName))
			}
			// on_event requires a channel field
			if dep.Condition == ConditionOnEvent && dep.Channel == "" {
				errorf(path, "condition 'on_event' requires a 'channel' field")
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

	// -- Enum: defaults.policy.default --
	if p.Defaults.Policy.Default != "" && !validPolicyDefaults[p.Defaults.Policy.Default] {
		errorf("defaults.policy.default",
			fmt.Sprintf("unknown policy default %q (valid: allow-all, balanced, deny-all)",
				p.Defaults.Policy.Default))
	}

	// -- Channel validation --
	channelSet := validateChannels(p, errorf)
	validateChannelRefs(p, channelSet, agentNames, errorf)

	// -- Reach validation --
	validateReach(p, agentNames, errorf)

	// -- DAG acyclicity --
	if cycle := detectCycle(p.Agents); cycle != nil {
		errorf("agents", fmt.Sprintf("dependency cycle detected: %s", strings.Join(cycle, " -> ")))
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
//	HOST_IP:HOST_PORT:SANDBOX_PORT[/PROTO]  (IPv4 only)
//
// Note: IPv6 host addresses (e.g. [::1]:8080:8080) are not supported in v0
// because the colon-split parser cannot distinguish IPv6 colons from field
// separators. Use IPv4 or leave the host IP blank (all-interfaces).
func validatePortSpec(spec string) error {
	origSpec := spec // preserve the original for error messages
	// Strip optional protocol suffix.
	proto := ""
	if idx := strings.LastIndex(spec, "/"); idx >= 0 {
		proto = spec[idx+1:]
		spec = spec[:idx]
		if proto != "tcp" && proto != "udp" && proto != "sctp" {
			return fmt.Errorf("unknown protocol %q in port spec %q (valid: tcp, udp, sctp)", proto, origSpec)
		}
	}

	parts := strings.Split(spec, ":")
	var hostPort, sandboxPort string
	switch len(parts) {
	case 2: // HOST_PORT:SANDBOX_PORT
		hostPort = parts[0]
		sandboxPort = parts[1]
	case 3: // HOST_IP:HOST_PORT:SANDBOX_PORT (IPv4 only)
		hostPort = parts[1]
		sandboxPort = parts[2]
	default:
		return fmt.Errorf("invalid port spec %q: expected HOST_PORT:SANDBOX_PORT or HOST_IP:HOST_PORT:SANDBOX_PORT (IPv6 not supported in v0)", origSpec)
	}

	if hostPort == "" {
		return fmt.Errorf("host port is required in port spec %q", origSpec)
	}
	if n, err := strconv.Atoi(hostPort); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid host port %q in port spec %q (must be 1-65535)", hostPort, origSpec)
	}
	if sandboxPort == "" {
		return fmt.Errorf("sandbox port is required in port spec %q", origSpec)
	}
	if n, err := strconv.Atoi(sandboxPort); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid sandbox port %q in port spec %q (must be 1-65535)", sandboxPort, origSpec)
	}
	return nil
}

// detectCycle uses DFS to find a cycle in the depends_on graph.
// Agent names are iterated in sorted order to produce a deterministic cycle
// path for reproducible error messages.
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
			// Sort dep names for deterministic traversal order.
			depNames := make([]string, 0, len(agent.DependsOn))
			for dep := range agent.DependsOn {
				depNames = append(depNames, dep)
			}
			sort.Strings(depNames)
			for _, dep := range depNames {
				switch state[dep] {
				case visiting:
					// Found a cycle -- extract it from the stack.
					for i, n := range stack {
						if n == dep {
							cycle := make([]string, len(stack)-i+1)
							copy(cycle, stack[i:])
							cycle[len(cycle)-1] = dep // close the loop
							return cycle
						}
					}
					return []string{dep, dep} // fallback (should not happen)
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

	// Sort agent names for deterministic start order.
	names := make([]string, 0, len(agents))
	for name := range agents {
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

// channelNameRe is the pattern a channel name must match.
var channelNameRe = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9]+)*$`)

// validateChannels validates the top-level channels[] list:
//   - each name must match channelNameRe
//
// Returns a set of valid channel names for reference-integrity checks.
func validateChannels(p *Project, errorf func(path, msg string)) map[string]struct{} {
	set := make(map[string]struct{}, len(p.Channels))
	for i, ch := range p.Channels {
		path := fmt.Sprintf("channels[%d]", i)
		if !channelNameRe.MatchString(ch) {
			errorf(path, fmt.Sprintf("channel name %q is invalid (must match [a-z0-9]+(\\.[a-z0-9]+)*)", ch))
			continue
		}
		if _, dup := set[ch]; dup {
			errorf(path, fmt.Sprintf("duplicate channel name %q", ch))
			continue
		}
		set[ch] = struct{}{}
	}
	return set
}

// validateChannelRefs checks that every emits/subscribes/depends_on.channel
// reference resolves to a declared channel, and enforces the single-emitter
// rule (v0: at most one agent emits each channel).
func validateChannelRefs(p *Project, channelSet map[string]struct{}, agentNames []string, errorf func(path, msg string)) {
	// emitterOf maps channel -> agent name (first agent that emits it).
	emitterOf := make(map[string]string)

	for _, name := range agentNames {
		agent := p.Agents[name]
		base := "agents." + name

		for i, ch := range agent.Emits {
			path := fmt.Sprintf("%s.emits[%d]", base, i)
			if _, ok := channelSet[ch]; !ok {
				errorf(path, fmt.Sprintf("channel %q is not declared in channels[]", ch))
				continue
			}
			// Single-emitter rule.
			if prev, dup := emitterOf[ch]; dup {
				errorf(path, fmt.Sprintf("channel %q already emitted by agent %q (v0: single emitter per channel)", ch, prev))
			} else {
				emitterOf[ch] = name
			}
		}

		for i, ch := range agent.Subscribes {
			path := fmt.Sprintf("%s.subscribes[%d]", base, i)
			if _, ok := channelSet[ch]; !ok {
				errorf(path, fmt.Sprintf("channel %q is not declared in channels[]", ch))
			}
		}

		// depends_on.channel references.
		depNames := make([]string, 0, len(agent.DependsOn))
		for depName := range agent.DependsOn {
			depNames = append(depNames, depName)
		}
		sort.Strings(depNames)
		for _, depName := range depNames {
			dep := agent.DependsOn[depName]
			if dep.Channel == "" {
				continue
			}
			path := base + ".depends_on." + depName
			if _, ok := channelSet[dep.Channel]; !ok {
				errorf(path, fmt.Sprintf("depends_on channel %q is not declared in channels[]", dep.Channel))
			}
		}
	}
}

// validateReach checks each agent's reach[] entries:
//   - format: "<agent-id>:<port>" where port is a decimal integer
//   - the target agent must be defined
//   - the port must appear in the target agent's declared ports
//
// validateReach is called from Validate; agentNames must be pre-sorted.
func validateReach(p *Project, agentNames []string, errorf func(path, msg string)) {
	for _, name := range agentNames {
		agent := p.Agents[name]
		base := "agents." + name

		for i, r := range agent.Reach {
			path := fmt.Sprintf("%s.reach[%d]", base, i)

			// Expect "<agent>:<port>".
			colon := strings.LastIndex(r, ":")
			if colon < 0 {
				errorf(path, fmt.Sprintf("reach entry %q is invalid: expected <agent>:<port>", r))
				continue
			}
			targetAgent := r[:colon]
			portStr := r[colon+1:]

			if targetAgent == "" {
				errorf(path, fmt.Sprintf("reach entry %q: agent name is empty", r))
				continue
			}
			target, ok := p.Agents[targetAgent]
			if !ok {
				errorf(path, fmt.Sprintf("reach entry %q: agent %q is not defined", r, targetAgent))
				continue
			}
			// The port must appear as the sandbox port in the target's ports list.
			if !agentPublishesPort(target.Ports, portStr) {
				errorf(path, fmt.Sprintf(
					"reach entry %q: agent %q does not publish sandbox port %s (declared ports: %v)",
					r, targetAgent, portStr, target.Ports,
				))
			}
		}
	}
}

// agentPublishesPort returns true if any port spec in specs has sandboxPort as its sandbox port.
func agentPublishesPort(specs []string, sandboxPort string) bool {
	for _, spec := range specs {
		// Strip proto suffix.
		if idx := strings.LastIndex(spec, "/"); idx >= 0 {
			spec = spec[:idx]
		}
		parts := strings.Split(spec, ":")
		var sp string
		switch len(parts) {
		case 2:
			sp = parts[1]
		case 3:
			sp = parts[2]
		default:
			continue
		}
		if sp == sandboxPort {
			return true
		}
	}
	return false
}
