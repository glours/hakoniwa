package config

// PolicyDefault is the allowed set of values for defaults.policy.default.
type PolicyDefault string

const (
	PolicyDefaultAllowAll PolicyDefault = "allow-all"
	PolicyDefaultBalanced PolicyDefault = "balanced"
	PolicyDefaultDenyAll  PolicyDefault = "deny-all"
)

// DependsOnCondition specifies when a dependency is considered satisfied.
type DependsOnCondition string

const (
	ConditionCreated   DependsOnCondition = "created"
	ConditionRunning   DependsOnCondition = "running"
	ConditionCompleted DependsOnCondition = "completed"
	ConditionOnEvent   DependsOnCondition = "on_event"
)

// Secret describes a secret to be injected into a sandbox.
// The Value field is a shell command whose stdout is the secret value.
type Secret struct {
	Placeholder string `yaml:"placeholder,omitempty"`
	Value       string `yaml:"value"`
	Host        string `yaml:"host,omitempty"`
	Env         string `yaml:"env,omitempty"`
	Optional    bool   `yaml:"optional,omitempty"`
}

// Resources holds optional resource limits for a sandbox.
// Memory is in MB.
type Resources struct {
	CPUs   float64 `yaml:"cpus,omitempty"`
	Memory int     `yaml:"memory,omitempty"`
}

// NetworkPolicy specifies per-agent egress allow/deny rules.
type NetworkPolicy struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

// Policy holds all policy settings for an agent or the project defaults.
// Default is only valid at the project level (inside defaults.policy).
type Policy struct {
	Default       PolicyDefault `yaml:"default,omitempty"`
	AllowWidening bool          `yaml:"allow_widening,omitempty"`
	Network       NetworkPolicy `yaml:"network,omitempty"`
}

// DependsOnEntry describes a single depends_on edge to another agent.
type DependsOnEntry struct {
	Condition DependsOnCondition `yaml:"condition"`
	Channel   string             `yaml:"channel,omitempty"`
}

// Agent represents a single agent block in the project file.
// Field names mirror .sbxenv where applicable.
type Agent struct {
	// .sbxenv fields
	Agent     string    `yaml:"agent,omitempty"`
	Command   string    `yaml:"command,omitempty"` // alias for Agent
	Template  string    `yaml:"template,omitempty"`
	Resources Resources `yaml:"resources,omitempty"`
	Ports     []string  `yaml:"ports,omitempty"`
	Secrets   []Secret  `yaml:"secrets,omitempty"`
	// Credentials is an alias for Secrets (sbxenv compat)
	Credentials []Secret `yaml:"credentials,omitempty"`
	Kits        []string `yaml:"kits,omitempty"`
	Policy      Policy   `yaml:"policy,omitempty"`

	// Net-new Hakoniwa fields
	Emits      []string                  `yaml:"emits,omitempty"`
	Subscribes []string                  `yaml:"subscribes,omitempty"`
	DependsOn  map[string]DependsOnEntry `yaml:"depends_on,omitempty"`
	Reach      []string                  `yaml:"reach,omitempty"`
}

// Defaults holds project-level defaults merged into every agent.
type Defaults struct {
	Policy    Policy   `yaml:"policy,omitempty"`
	Kits      []string `yaml:"kits,omitempty"`
	Secrets   []Secret `yaml:"secrets,omitempty"`
	Resources Resources `yaml:"resources,omitempty"`
	Template  string   `yaml:"template,omitempty"`
}

// Project is the top-level parsed representation of a hakoniwa.yaml /
// hako.yaml project file.
type Project struct {
	Name     string            `yaml:"name"`
	Defaults Defaults          `yaml:"defaults,omitempty"`
	Channels []string          `yaml:"channels,omitempty"`
	Agents   map[string]*Agent `yaml:"agents"`
}
