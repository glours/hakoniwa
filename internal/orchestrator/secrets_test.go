package orchestrator

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
)

func TestApplySecretsNoOp(t *testing.T) {
	s := newFakeState()
	o, _ := newTestOrchestrator(s, "proj")

	project := minimalProject("proj", "worker", "claude")

	if err := o.ApplySecretsAndPolicy(context.Background(), project); err != nil {
		t.Fatalf("ApplySecretsAndPolicy with no secrets/policy: %v", err)
	}
}

func TestApplySecretsResolveValue(t *testing.T) {
	s := newFakeState()
	o, _ := newTestOrchestrator(s, "proj")

	var injectedValues []string
	o.Sbx = &captureSbx{
		SbxAdapter: o.Sbx,
		onSecret:   func(v string) { injectedValues = append(injectedValues, v) },
	}

	project := minimalProject("proj", "worker", "claude")
	project.Agents["worker"].Credentials = []config.Secret{
		{Value: "echo hello", Env: "TOKEN"},
	}

	if err := o.ApplySecretsAndPolicy(context.Background(), project); err != nil {
		t.Fatalf("ApplySecretsAndPolicy: %v", err)
	}
	if len(injectedValues) != 1 || injectedValues[0] != "hello" {
		t.Errorf("injected values = %v, want [hello]", injectedValues)
	}
}

func TestApplySecretsOptionalFailure(t *testing.T) {
	s := newFakeState()
	o, _ := newTestOrchestrator(s, "proj")

	project := minimalProject("proj", "worker", "claude")
	project.Agents["worker"].Secrets = []config.Secret{
		{Value: "exit 1", Env: "OPT_VAR", Optional: true},
		{Value: "echo tok", Env: "REQ_VAR"},
	}

	// Optional failure must not propagate.
	if err := o.ApplySecretsAndPolicy(context.Background(), project); err != nil {
		t.Fatalf("optional failure should not propagate: %v", err)
	}
}

func TestApplySecretsRequiredFailure(t *testing.T) {
	s := newFakeState()
	o, _ := newTestOrchestrator(s, "proj")

	project := minimalProject("proj", "worker", "claude")
	project.Agents["worker"].Secrets = []config.Secret{
		{Value: "exit 42", Env: "REQUIRED"},
	}

	if err := o.ApplySecretsAndPolicy(context.Background(), project); err == nil {
		t.Fatal("expected error for required secret failure")
	}
}

func TestWideningLintBlocked(t *testing.T) {
	s := newFakeState()
	o, _ := newTestOrchestrator(s, "proj")

	project := minimalProject("proj", "worker", "claude")
	project.Defaults.Policy.AllowWidening = false
	project.Agents["worker"].Policy = config.AgentPolicy{
		Network: config.NetworkPolicy{Allow: []string{"*.github.com"}},
	}

	err := o.ApplySecretsAndPolicy(context.Background(), project)
	if err == nil {
		t.Fatal("expected widening lint error")
	}
	if !strings.Contains(err.Error(), "allow_widening") {
		t.Errorf("expected 'allow_widening' in error, got: %v", err)
	}
}

func TestWideningLintPermitted(t *testing.T) {
	s := newFakeState()
	o, _ := newTestOrchestrator(s, "proj")

	project := minimalProject("proj", "worker", "claude")
	project.Defaults.Policy.AllowWidening = true
	project.Agents["worker"].Policy = config.AgentPolicy{
		Network: config.NetworkPolicy{Allow: []string{"*.github.com"}},
	}

	if err := o.ApplySecretsAndPolicy(context.Background(), project); err != nil {
		t.Fatalf("widening should be permitted: %v", err)
	}
}

func TestPolicyConvergeDenyRule(t *testing.T) {
	s := newFakeState()
	o, _ := newTestOrchestrator(s, "proj")

	var denyCalls []string
	o.Sbx = &captureSbx{
		SbxAdapter: o.Sbx,
		onDeny:     func(rule string) { denyCalls = append(denyCalls, rule) },
	}

	project := minimalProject("proj", "worker", "claude")
	project.Agents["worker"].Policy = config.AgentPolicy{
		Network: config.NetworkPolicy{Deny: []string{"*.telemetry.io"}},
	}

	if err := o.ApplySecretsAndPolicy(context.Background(), project); err != nil {
		t.Fatalf("ApplySecretsAndPolicy: %v", err)
	}
	if len(denyCalls) != 1 || !strings.Contains(denyCalls[0], "*.telemetry.io") {
		t.Errorf("deny calls = %v", denyCalls)
	}
}

func TestPolicySetDefault(t *testing.T) {
	s := newFakeState()
	o, _ := newTestOrchestrator(s, "proj")

	var defaultSet string
	o.Sbx = &captureSbx{
		SbxAdapter:      o.Sbx,
		onPolicyDefault: func(preset string) { defaultSet = preset },
	}

	project := minimalProject("proj", "worker", "claude")
	project.Defaults.Policy.Default = "deny-all"

	if err := o.ApplySecretsAndPolicy(context.Background(), project); err != nil {
		t.Fatalf("ApplySecretsAndPolicy: %v", err)
	}
	if defaultSet != "deny-all" {
		t.Errorf("policy default = %q, want deny-all", defaultSet)
	}
}

func TestPolicyAllowWithWidening(t *testing.T) {
	s := newFakeState()
	o, _ := newTestOrchestrator(s, "proj")

	var allowCalls []string
	o.Sbx = &captureSbx{
		SbxAdapter: o.Sbx,
		onAllow:    func(rule string) { allowCalls = append(allowCalls, rule) },
	}

	project := minimalProject("proj", "worker", "claude")
	project.Defaults.Policy.AllowWidening = true
	project.Agents["worker"].Policy = config.AgentPolicy{
		Network: config.NetworkPolicy{Allow: []string{"api.github.com"}},
	}

	if err := o.ApplySecretsAndPolicy(context.Background(), project); err != nil {
		t.Fatalf("ApplySecretsAndPolicy: %v", err)
	}
	if len(allowCalls) != 1 || !strings.Contains(allowCalls[0], "api.github.com") {
		t.Errorf("allow calls = %v", allowCalls)
	}
}

// ---------------------------------------------------------------------------
// captureSbx — wraps SbxAdapter and fires hooks for test observability.
// ---------------------------------------------------------------------------

type captureSbx struct {
	sandbox.SbxAdapter
	onSecret        func(value string)
	onDeny          func(rule string)
	onAllow         func(rule string)
	onPolicyDefault func(preset string)
}

func (c *captureSbx) SecretSetCustom(ctx context.Context, sbxName string, req sandbox.SecretSetRequest) error {
	if c.onSecret != nil {
		c.onSecret(req.Value)
	}
	return c.SbxAdapter.SecretSetCustom(ctx, sbxName, req)
}

func (c *captureSbx) PolicyDeny(ctx context.Context, sbxName, rule string) error {
	if c.onDeny != nil {
		c.onDeny(rule)
	}
	return c.SbxAdapter.PolicyDeny(ctx, sbxName, rule)
}

func (c *captureSbx) PolicyAllow(ctx context.Context, sbxName, rule string) error {
	if c.onAllow != nil {
		c.onAllow(rule)
	}
	return c.SbxAdapter.PolicyAllow(ctx, sbxName, rule)
}

func (c *captureSbx) PolicySetDefault(ctx context.Context, preset string) error {
	if c.onPolicyDefault != nil {
		c.onPolicyDefault(preset)
	}
	return c.SbxAdapter.PolicySetDefault(ctx, preset)
}

// newTestOrchestrator is defined in up_test.go (same package).
// minimalProject is also defined there.
// fakeState / fakeSbx / fakeClient are in up_test.go.
// This file adds only the secrets/policy-specific test helpers.

// io.Discard reference to avoid unused import.
var _ = io.Discard
