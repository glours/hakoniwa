package config

// EffectiveAgent is the fully-resolved agent configuration after merging
// project defaults into the per-agent block. It is the canonical view used
// by the orchestrator and validator.
type EffectiveAgent struct {
	// Identity
	Name string // the agent key from the YAML (e.g. "fixer")

	// Runtime (from .sbxenv fields, post-merge)
	AgentKind   string    // agent/command — the resolved kind
	Template    string    // container image override
	Resources   Resources // cpus/memory (project defaults inherited if agent omits)
	Ports       []string  // declared host→sandbox port mappings
	Kits        []string  // effective kits: defaults.kits ∪ agent.kits (deduped)
	Secrets     []Secret  // only secrets the agent opted in to (no implicit inherit)
	Credentials []Secret  // alias for Secrets (sbxenv compat); merged same way
	Policy      AgentPolicy // effective per-agent network policy

	// Net-new orchestration fields (passed through unchanged from the agent)
	Emits      []string
	Subscribes []string
	DependsOn  map[string]DependsOnEntry
	Reach      []string

	// Project-level policy (from defaults; shared, not per-agent)
	ProjectPolicyDefault PolicyDefault
	AllowWidening        bool
}

// ResolveAgents merges the project defaults into every agent and returns a
// map of agent-name → EffectiveAgent.
//
// Merge rules (design §6):
//   - Kits:      additive union — defaults.kits ∪ agent.kits, de-duplicated.
//   - Secrets:   strict per-agent scoping — an agent receives a secret only if
//     it explicitly lists that secret name/env in its own secrets/credentials.
//     defaults.secrets is never implicitly inherited.
//   - Resources: project defaults.resources are inherited if the agent does not
//     set the field (zero value = unset).
//   - Template:  same as resources — project default inherited if agent omits.
//   - Policy.Default / AllowWidening: project-level only (already enforced by
//     ProjectPolicy / AgentPolicy split); copied onto EffectiveAgent for easy
//     access by the orchestrator and widening lint.
func ResolveAgents(p *Project) map[string]*EffectiveAgent {
	out := make(map[string]*EffectiveAgent, len(p.Agents))
	for name, agent := range p.Agents {
		out[name] = resolveOne(name, agent, &p.Defaults)
	}
	return out
}

// resolveOne merges the project defaults into a single agent and returns the
// EffectiveAgent. All slice/map fields are defensively copied so the result is
// an independent snapshot — mutations to the original Agent do not affect the
// EffectiveAgent and vice versa.
func resolveOne(name string, agent *Agent, defaults *Defaults) *EffectiveAgent {
	ea := &EffectiveAgent{
		Name:                 name,
		Emits:                append([]string(nil), agent.Emits...),
		Subscribes:           append([]string(nil), agent.Subscribes...),
		DependsOn:            copyDependsOn(agent.DependsOn),
		Reach:                append([]string(nil), agent.Reach...),
		Ports:                append([]string(nil), agent.Ports...),
		Policy:               copyAgentPolicy(agent.Policy),
		ProjectPolicyDefault: defaults.Policy.Default,
		AllowWidening:        defaults.Policy.AllowWidening,
	}

	// agent/command: use whichever is set; command is an alias.
	ea.AgentKind = agent.Agent
	if ea.AgentKind == "" {
		ea.AgentKind = agent.Command
	}

	// Template: inherit from defaults if the agent did not set one.
	ea.Template = agent.Template
	if ea.Template == "" {
		ea.Template = defaults.Template
	}

	// Resources: inherit each sub-field from defaults if the agent left it zero.
	ea.Resources = mergeResources(defaults.Resources, agent.Resources)

	// Kits: additive union (defaults first, then agent), deduped.
	ea.Kits = unionStrings(defaults.Kits, agent.Kits)

	// Secrets: strict per-agent scoping — no implicit inheritance.
	// The agent receives only its own secrets/credentials.
	ea.Secrets = append([]Secret(nil), agent.Secrets...)
	ea.Credentials = append([]Secret(nil), agent.Credentials...)

	return ea
}

// mergeResources returns a Resources where each field falls back to the
// project default if the agent-level value is zero.
//
// Note: zero is the only "unset" sentinel for numeric fields (there is no
// pointer/optional type here). An explicit `cpus: 0` in YAML is therefore
// indistinguishable from an absent field and will inherit the project default.
// Intentional zero resources should be expressed by setting the project
// default to zero as well.
func mergeResources(projectDefault, agent Resources) Resources {
	r := agent
	if r.CPUs == 0 {
		r.CPUs = projectDefault.CPUs
	}
	if r.Memory == 0 {
		r.Memory = projectDefault.Memory
	}
	return r
}

// copyAgentPolicy returns a deep copy of an AgentPolicy, ensuring the
// Allow/Deny slices in the embedded NetworkPolicy are independent.
func copyAgentPolicy(p AgentPolicy) AgentPolicy {
	return AgentPolicy{
		Network: NetworkPolicy{
			Allow: append([]string(nil), p.Network.Allow...),
			Deny:  append([]string(nil), p.Network.Deny...),
		},
	}
}

// copyDependsOn makes a shallow copy of a depends_on map so the EffectiveAgent
// owns its own map header (appending/deleting won't affect the original Agent).
func copyDependsOn(m map[string]DependsOnEntry) map[string]DependsOnEntry {
	if m == nil {
		return nil
	}
	out := make(map[string]DependsOnEntry, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// unionStrings returns the ordered union of a and b with duplicates removed.
// Elements from a come first, then new elements from b.
func unionStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	result := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	for _, s := range b {
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}
