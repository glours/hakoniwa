package orchestrator

import (
	"context"
	"testing"

	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

func TestDownRemovesSandboxes(t *testing.T) {
	s := newFakeState()
	// Pre-populate two project sandboxes and one unrelated one.
	s.sandboxes["proj-alpha"] = &sandbox.SandboxInfo{
		Name: "proj-alpha", Status: sandboxapi.SandboxInfoStatusRunning,
	}
	s.sandboxes["proj-beta"] = &sandbox.SandboxInfo{
		Name: "proj-beta", Status: sandboxapi.SandboxInfoStatusRunning,
	}
	s.sandboxes["other-gamma"] = &sandbox.SandboxInfo{
		Name: "other-gamma", Status: sandboxapi.SandboxInfoStatusRunning,
	}

	o, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}

	if err := o.Down(context.Background()); err != nil {
		t.Fatalf("Down: %v", err)
	}

	// Project sandboxes removed.
	if _, ok := s.sandboxes["proj-alpha"]; ok {
		t.Error("proj-alpha should be removed")
	}
	if _, ok := s.sandboxes["proj-beta"]; ok {
		t.Error("proj-beta should be removed")
	}
	// Unrelated sandbox untouched.
	if _, ok := s.sandboxes["other-gamma"]; !ok {
		t.Error("other-gamma should not be removed")
	}
}

func TestDownIdempotent(t *testing.T) {
	s := newFakeState()
	o, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}

	// First Down on empty state — should be a no-op.
	if err := o.Down(context.Background()); err != nil {
		t.Fatalf("first Down (no sandboxes): %v", err)
	}

	// Populate and tear down.
	s.sandboxes["proj-worker"] = &sandbox.SandboxInfo{
		Name: "proj-worker", Status: sandboxapi.SandboxInfoStatusRunning,
	}
	if err := o.Down(context.Background()); err != nil {
		t.Fatalf("second Down: %v", err)
	}

	// Third Down — already empty, must be no-op.
	if err := o.Down(context.Background()); err != nil {
		t.Fatalf("third Down (already empty): %v", err)
	}
}

func TestDownUnpublishesPorts(t *testing.T) {
	s := newFakeState()
	s.sandboxes["proj-web"] = &sandbox.SandboxInfo{
		Name: "proj-web", Status: sandboxapi.SandboxInfoStatusRunning,
	}
	proto := sandboxapi.PublishedPortProtocol("tcp")
	s.publishedPorts["proj-web"] = []sandbox.PublishedPort{
		{HostIp: "127.0.0.1", HostPort: 8080, SandboxPort: 8080, Protocol: proto},
	}

	o, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}

	if err := o.Down(context.Background()); err != nil {
		t.Fatalf("Down: %v", err)
	}

	if len(s.publishedPorts["proj-web"]) != 0 {
		t.Errorf("expected ports unpublished, got %v", s.publishedPorts["proj-web"])
	}
}

func TestDownNoProjectSandboxes(t *testing.T) {
	s := newFakeState()
	s.sandboxes["other-x"] = &sandbox.SandboxInfo{
		Name: "other-x", Status: sandboxapi.SandboxInfoStatusRunning,
	}
	o, err := newTestOrchestrator(s, "proj")
	if err != nil {
		t.Fatal(err)
	}

	if err := o.Down(context.Background()); err != nil {
		t.Fatalf("Down: %v", err)
	}
	// other-x must still be there.
	if _, ok := s.sandboxes["other-x"]; !ok {
		t.Error("other-x should not have been removed")
	}
}
