package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
)

// ---------------------------------------------------------------------------
// fakeSession — implements sandbox.Session for emit tests
// ---------------------------------------------------------------------------

type fakeSession struct {
	execID   string
	output   string // written to stdout by Stream
	exitCode int
	exitErr  error // returned by ExitCode if non-nil
}

func (f *fakeSession) ExecID() string { return f.execID }

func (f *fakeSession) Stream(stdout, _ io.Writer) error {
	_, _ = stdout.Write([]byte(f.output))
	return nil
}

func (f *fakeSession) ExitCode(_ context.Context) (int, error) {
	if f.exitErr != nil {
		return 0, f.exitErr
	}
	return f.exitCode, nil
}

func (f *fakeSession) Close() error { return nil }

// ---------------------------------------------------------------------------
// fakeStager — implements sandbox.FileStager for emit tests
// ---------------------------------------------------------------------------

type fakeStager struct {
	files  map[string][]byte // key = "<sbxName>/<path>"
	staged map[string][]byte
}

func newFakeStager() *fakeStager {
	return &fakeStager{
		files:  make(map[string][]byte),
		staged: make(map[string][]byte),
	}
}

func (f *fakeStager) key(sbx, path string) string { return sbx + ":" + path }

func (f *fakeStager) setFile(sbx, path string, data []byte) {
	f.files[f.key(sbx, path)] = data
}

func (f *fakeStager) GetFile(_ context.Context, name, path string) ([]byte, error) {
	k := f.key(name, path)
	data, ok := f.files[k]
	if !ok {
		return nil, &sandbox.NotFoundError{Resource: "path " + path}
	}
	return data, nil
}

func (f *fakeStager) PutFile(_ context.Context, name, path string, data []byte) error {
	f.staged[f.key(name, path)] = data
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newEmitDetector(reg *ChannelRegistry, stager sandbox.FileStager) *EmitDetector {
	return &EmitDetector{
		Registry: reg,
		Stager:   stager,
		Out:      io.Discard,
	}
}

func simpleEA(emits ...string) *config.EffectiveAgent {
	return &config.EffectiveAgent{
		AgentKind: "shell",
		Emits:     emits,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDriveAndEmitFiresChannel(t *testing.T) {
	reg := NewChannelRegistry([]string{"ready"}, map[string]string{"ready": "alpha"})
	stager := newFakeStager()
	payload := json.RawMessage(`{"status":"done"}`)
	stager.setFile("proj-alpha", sandbox.HakoOutPath("ready"), payload)

	det := newEmitDetector(reg, stager)
	sess := &fakeSession{execID: "e1", exitCode: 0}

	if err := det.DriveAndEmit(context.Background(), "alpha", "proj-alpha", simpleEA("ready"), sess); err != nil {
		t.Fatalf("DriveAndEmit: %v", err)
	}
	if !reg.IsFired("ready") {
		t.Error("channel 'ready' should be fired")
	}
	p, ok := reg.Payload("ready")
	if !ok || string(p) != string(payload) {
		t.Errorf("payload = %s, want %s", p, payload)
	}
}

func TestDriveAndEmitNoEmits(t *testing.T) {
	reg := NewChannelRegistry(nil, nil)
	stager := newFakeStager()

	det := newEmitDetector(reg, stager)
	sess := &fakeSession{exitCode: 0}
	if err := det.DriveAndEmit(context.Background(), "alpha", "proj-alpha", simpleEA(), sess); err != nil {
		t.Fatalf("DriveAndEmit (no emits): %v", err)
	}
}

func TestDriveAndEmitNonZeroExit(t *testing.T) {
	reg := NewChannelRegistry([]string{"ch"}, nil)
	stager := newFakeStager()

	det := newEmitDetector(reg, stager)
	sess := &fakeSession{exitCode: 1}
	err := det.DriveAndEmit(context.Background(), "alpha", "proj-alpha", simpleEA("ch"), sess)
	if err == nil {
		t.Fatal("expected error for non-zero exit code")
	}
	if !strings.Contains(err.Error(), "exited with code 1") {
		t.Errorf("unexpected error: %v", err)
	}
	if reg.IsFired("ch") {
		t.Error("channel should not fire on non-zero exit")
	}
}

func TestDriveAndEmitMissingOutputFile(t *testing.T) {
	reg := NewChannelRegistry([]string{"ready"}, map[string]string{"ready": "alpha"})
	stager := newFakeStager() // no file set → GetFile returns NotFound

	det := newEmitDetector(reg, stager)
	sess := &fakeSession{exitCode: 0}
	err := det.DriveAndEmit(context.Background(), "alpha", "proj-alpha", simpleEA("ready"), sess)
	if err == nil {
		t.Fatal("expected error when output file is missing")
	}
	if !strings.Contains(err.Error(), "ready") {
		t.Errorf("error should mention channel name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "absent") {
		t.Errorf("error should mention 'absent', got: %v", err)
	}
}

func TestDriveAndEmitStreamOutputCaptured(t *testing.T) {
	reg := NewChannelRegistry(nil, nil)
	stager := newFakeStager()

	var out bytes.Buffer
	det := &EmitDetector{Registry: reg, Stager: stager, Out: &out}
	sess := &fakeSession{output: "agent output here", exitCode: 0}
	_ = det.DriveAndEmit(context.Background(), "agent", "proj-agent", simpleEA(), sess)

	if !strings.Contains(out.String(), "agent output here") {
		t.Errorf("output not captured: %q", out.String())
	}
}

func TestDriveAndEmitMultipleChannels(t *testing.T) {
	reg := NewChannelRegistry([]string{"ch1", "ch2"}, nil)
	stager := newFakeStager()
	stager.setFile("proj-a", sandbox.HakoOutPath("ch1"), json.RawMessage(`"payload1"`))
	stager.setFile("proj-a", sandbox.HakoOutPath("ch2"), json.RawMessage(`"payload2"`))

	det := newEmitDetector(reg, stager)
	sess := &fakeSession{exitCode: 0}
	if err := det.DriveAndEmit(context.Background(), "a", "proj-a", simpleEA("ch1", "ch2"), sess); err != nil {
		t.Fatalf("DriveAndEmit multi-channel: %v", err)
	}
	if !reg.IsFired("ch1") || !reg.IsFired("ch2") {
		t.Error("both channels should be fired")
	}
}
