package orchestrator

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// countingDriver returns 404 for the first `failCount` calls to
// AttachAgentSession, then succeeds.
type countingDriver struct {
	failCount int
	calls     int
}

func (d *countingDriver) AttachAgentSession(_ context.Context, name string, _ sandboxapi.AgentSessionRequest) (sandbox.Session, error) {
	d.calls++
	if d.calls <= d.failCount {
		return nil, &sandbox.NotFoundError{Resource: "sandbox " + name}
	}
	return &fakeSession{exitCode: 0}, nil
}

func TestAttachSessionWithRetrySucceedsOnFirstAttempt(t *testing.T) {
	d := &countingDriver{failCount: 0}
	o := &Orchestrator{
		Driver:             d,
		Out:                io.Discard,
		SessionRetryDelays: []time.Duration{time.Millisecond},
	}
	sess, err := o.attachSessionWithRetry(context.Background(), "agent", "proj-agent", sandboxapi.AgentSessionRequest{})
	if err != nil {
		t.Fatalf("expected success on first attempt, got: %v", err)
	}
	if sess == nil {
		t.Fatal("expected non-nil session")
	}
	if d.calls != 1 {
		t.Errorf("calls = %d, want 1", d.calls)
	}
}

func TestAttachSessionWithRetrySucceedsAfterTwoFailures(t *testing.T) {
	d := &countingDriver{failCount: 2}
	o := &Orchestrator{
		Driver:             d,
		Out:                io.Discard,
		SessionRetryDelays: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
	}
	sess, err := o.attachSessionWithRetry(context.Background(), "agent", "proj-agent", sandboxapi.AgentSessionRequest{})
	if err != nil {
		t.Fatalf("expected success after 2 retries, got: %v", err)
	}
	if sess == nil {
		t.Fatal("expected non-nil session")
	}
	if d.calls != 3 {
		t.Errorf("calls = %d, want 3 (2 failures + 1 success)", d.calls)
	}
}

func TestAttachSessionWithRetryExhaustsAndErrors(t *testing.T) {
	delays := []time.Duration{time.Millisecond, time.Millisecond}
	d := &countingDriver{failCount: 999} // always fails
	o := &Orchestrator{
		Driver:             d,
		Out:                io.Discard,
		SessionRetryDelays: delays,
	}
	_, err := o.attachSessionWithRetry(context.Background(), "agent", "proj-agent", sandboxapi.AgentSessionRequest{})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	wantAttempts := len(delays) + 1
	if d.calls != wantAttempts {
		t.Errorf("calls = %d, want %d", d.calls, wantAttempts)
	}
	if !strings.Contains(err.Error(), "not ready after") {
		t.Errorf("error should mention 'not ready after', got: %v", err)
	}
}

func TestAttachSessionWithRetryDoesNotRetryNon404(t *testing.T) {
	// A non-404 error (e.g. 422 start-script-missing) must not be retried.
	alwaysBadDriver := &alwaysErrorDriver{err: &sandbox.NotFoundError{Resource: "this is 404"}}
	alwaysBadDriver.notFound = false // override: non-404 error
	o := &Orchestrator{
		Driver:             alwaysBadDriver,
		Out:                io.Discard,
		SessionRetryDelays: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
	}
	_, err := o.attachSessionWithRetry(context.Background(), "agent", "proj-agent", sandboxapi.AgentSessionRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if alwaysBadDriver.calls != 1 {
		t.Errorf("calls = %d, want 1 (non-404 must not be retried)", alwaysBadDriver.calls)
	}
}

func TestAttachSessionWithRetryContextCancellation(t *testing.T) {
	d := &countingDriver{failCount: 999}
	o := &Orchestrator{
		Driver:             d,
		Out:                io.Discard,
		SessionRetryDelays: []time.Duration{10 * time.Second, 10 * time.Second},
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := o.attachSessionWithRetry(ctx, "agent", "proj-agent", sandboxapi.AgentSessionRequest{})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestAttachSessionWithRetryLogsRetries(t *testing.T) {
	d := &countingDriver{failCount: 2}
	var buf strings.Builder
	o := &Orchestrator{
		Driver:             d,
		Out:                &buf,
		SessionRetryDelays: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
	}
	_, err := o.attachSessionWithRetry(context.Background(), "myagent", "proj-myagent", sandboxapi.AgentSessionRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "myagent") || !strings.Contains(buf.String(), "retrying") {
		t.Errorf("expected retry log messages, got: %q", buf.String())
	}
}

// alwaysErrorDriver returns a non-NotFound error to test no-retry behaviour.
type alwaysErrorDriver struct {
	calls    int
	err      error
	notFound bool // if false, returns a non-404-wrapped error
}

func (d *alwaysErrorDriver) AttachAgentSession(_ context.Context, name string, _ sandboxapi.AgentSessionRequest) (sandbox.Session, error) {
	d.calls++
	if d.notFound {
		return nil, &sandbox.NotFoundError{Resource: "sandbox " + name}
	}
	// Return a plain (non-NotFound) error to verify no retry.
	return nil, &startScriptMissingError{}
}

type startScriptMissingError struct{}

func (e *startScriptMissingError) Error() string {
	return "422: start script missing"
}
