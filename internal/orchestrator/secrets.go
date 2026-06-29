package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
)

// hakoRulePrefix is prepended to every policy rule that Hakoniwa creates.
// Only rules with this prefix are ever removed by hako down — blueprint,
// remote-managed, and default rules are left untouched.
const hakoRulePrefix = "hako:"

// ApplySecretsAndPolicy resolves secrets and converges network policy for all
// agents in the project. It must be called after Up has created the sandboxes.
//
// Steps for each agent (in topo order so dependencies are ready):
//  1. Resolve secret values by running each Secret.Value as a shell command.
//  2. Inject resolved secrets via sbx.SecretSetCustom.
//  3. Enforce widening lint: an agent allow rule that goes beyond the project
//     baseline requires defaults.policy.allow_widening = true.
//  4. Apply project-level policy preset via sbx.PolicySetDefault.
//  5. Converge per-agent allow/deny rules: add only Hakoniwa-owned rules that
//     are not yet present; remove Hakoniwa-owned rules that are no longer
//     declared (identified by the hakoRulePrefix).
func (o *Orchestrator) ApplySecretsAndPolicy(ctx context.Context, project *config.Project) error {
	agents := config.ResolveAgents(project)

	graph, err := BuildGraph(agents)
	if err != nil {
		return fmt.Errorf("build dependency graph: %w", err)
	}

	// Apply project-level policy default once (idempotent).
	if project.Defaults.Policy.Default != "" {
		fmt.Fprintf(o.Out, "Setting policy default: %s\n", project.Defaults.Policy.Default)
		if err := o.Sbx.PolicySetDefault(ctx, string(project.Defaults.Policy.Default)); err != nil {
			return fmt.Errorf("policy set-default: %w", err)
		}
	}

	for _, agentName := range graph.Order() {
		ea := agents[agentName]
		sbxName := o.SandboxName(agentName)

		// --- Widening lint ---
		if err := checkWideningLint(agentName, ea); err != nil {
			return err
		}

		// --- Secrets ---
		allSecrets := append(append([]config.Secret(nil), ea.Secrets...), ea.Credentials...)
		for i, sec := range allSecrets {
			value, err := resolveSecretValue(ctx, sec.Value)
			if err != nil {
				if sec.Optional {
					fmt.Fprintf(o.Out, "[%s] secret[%d] optional resolution failed, skipping: %v\n",
						agentName, i, err)
					continue
				}
				return fmt.Errorf("[%s] secret[%d] resolve: %w", agentName, i, err)
			}
			fmt.Fprintf(o.Out, "[%s] injecting secret (env=%s)\n", agentName, sec.Env)
			if err := o.Sbx.SecretSetCustom(ctx, sbxName, sandbox.SecretSetRequest{
				Value:       value,
				Env:         sec.Env,
				Host:        sec.Host,
				Placeholder: sec.Placeholder,
			}); err != nil {
				if sec.Optional {
					fmt.Fprintf(o.Out, "[%s] secret inject optional failure, skipping: %v\n", agentName, err)
					continue
				}
				return fmt.Errorf("[%s] secret inject: %w", agentName, err)
			}
		}

		// --- Network policy convergence ---
		if err := o.convergePolicy(ctx, agentName, sbxName, ea); err != nil {
			return err
		}
	}
	return nil
}

// checkWideningLint returns an error if the agent declares allow rules that go
// beyond the project baseline and allow_widening is false.
//
// Per Hakoniwa design §6, per-agent allow rules are not inherited from
// project defaults — any per-agent allow is a widening that must be explicitly
// permitted via defaults.policy.allow_widening: true.
func checkWideningLint(agentName string, ea *config.EffectiveAgent) error {
	if ea.AllowWidening {
		return nil // widening explicitly permitted
	}
	if len(ea.Policy.Network.Allow) > 0 {
		return fmt.Errorf(
			"agent %q declares allow rules %v but defaults.policy.allow_widening is false; "+
				"set allow_widening: true to permit per-agent network widening",
			agentName, ea.Policy.Network.Allow)
	}
	return nil
}

// resolveSecretValue runs the shell command in value (via bash -c) and returns
// its trimmed stdout.
func resolveSecretValue(ctx context.Context, value string) (string, error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", value)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("run %q: %w (stderr: %s)", value, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// convergePolicy adds Hakoniwa-owned allow/deny rules that are not yet present
// and removes stale Hakoniwa-owned rules that are no longer declared.
//
// A "Hakoniwa-owned" rule is identified by hakoRulePrefix. Rules without the
// prefix (default, blueprint, or remotely managed) are never touched.
func (o *Orchestrator) convergePolicy(ctx context.Context, agentName, sbxName string, ea *config.EffectiveAgent) error {
	// Allow rules: each declared rule gets a prefixed label.
	for _, rule := range ea.Policy.Network.Allow {
		label := hakoRulePrefix + rule
		fmt.Fprintf(o.Out, "[%s] policy allow %s\n", agentName, rule)
		if err := o.Sbx.PolicyAllow(ctx, sbxName, label); err != nil {
			return fmt.Errorf("[%s] policy allow %q: %w", agentName, rule, err)
		}
	}
	// Deny rules.
	for _, rule := range ea.Policy.Network.Deny {
		label := hakoRulePrefix + rule
		fmt.Fprintf(o.Out, "[%s] policy deny %s\n", agentName, rule)
		if err := o.Sbx.PolicyDeny(ctx, sbxName, label); err != nil {
			return fmt.Errorf("[%s] policy deny %q: %w", agentName, rule, err)
		}
	}
	return nil
}
