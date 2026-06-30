// Package fake provides an in-memory implementation of the sandbox interfaces
// (Client, SbxAdapter, SessionDriver, FileStager) for use in tests that must
// run without a real sandboxd daemon or sbx CLI binary.
//
// Usage pattern:
//
//	b := fake.NewBackend()
//	b.ConfigureSession("proj-agent", &fake.SessionConfig{
//	    Output:   "agent ran\n",
//	    ExitCode: 0,
//	    Emits: map[string]json.RawMessage{
//	        "my.channel": json.RawMessage(`{"status":"done"}`),
//	    },
//	})
//	orch, _ := orchestrator.NewOrchestrator(b, b, "proj", io.Discard)
//	orch.Driver = b
//	orch.Stager = b
//	orch.Up(ctx, project)
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// SessionConfig controls the behaviour of a fake agent session returned by
// Backend.AttachAgentSession.
type SessionConfig struct {
	// Output is written verbatim to stdout when Session.Stream is called.
	Output string
	// ExitCode is returned by Session.ExitCode.
	ExitCode int
	// Emits maps channel name -> JSON payload to write into the backend's
	// file store at .hako/out/<channel>.json before Stream returns, so that
	// orchestrator emit-detection can read them via GetFile.
	Emits map[string]json.RawMessage
}

// Backend is a thread-safe, in-memory implementation of sandbox.Client,
// sandbox.SbxAdapter, sandbox.SessionDriver, and sandbox.FileStager.
//
// All maps are initialised by NewBackend and may be mutated freely between
// calls to simulate different daemon states.
type Backend struct {
	mu       sync.RWMutex
	boxes    map[string]*sandboxapi.SandboxInfo // name → info
	ports    map[string][]sandbox.PublishedPort // name → bindings
	files    map[string]map[string][]byte       // name → path → bytes
	sessions map[string]*SessionConfig          // name → session cfg
	creates  map[string]int                     // name → create count

	// NeverRun prevents StartSandbox from transitioning the sandbox to
	// "running", which exercises wait-running timeout paths.
	NeverRun bool

	// FailCreate, if set for a sandbox name, makes Create return that error.
	FailCreate map[string]error
}

// compile-time interface checks
var (
	_ sandbox.Client        = (*Backend)(nil)
	_ sandbox.SbxAdapter    = (*Backend)(nil)
	_ sandbox.SessionDriver = (*Backend)(nil)
	_ sandbox.FileStager    = (*Backend)(nil)
)

// NewBackend returns a Backend ready for use.
func NewBackend() *Backend {
	return &Backend{
		boxes:      make(map[string]*sandboxapi.SandboxInfo),
		ports:      make(map[string][]sandbox.PublishedPort),
		files:      make(map[string]map[string][]byte),
		sessions:   make(map[string]*SessionConfig),
		creates:    make(map[string]int),
		FailCreate: make(map[string]error),
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// ConfigureSession sets the session behaviour for the named sandbox.
// If not configured, sessions succeed with no output and no emits.
func (b *Backend) ConfigureSession(sandboxName string, cfg *SessionConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions[sandboxName] = cfg
}

// CreateCalls returns how many times Create was called for sandboxName.
func (b *Backend) CreateCalls(sandboxName string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.creates[sandboxName]
}

// SandboxStatus returns the current status of sandboxName, or "" if absent.
func (b *Backend) SandboxStatus(sandboxName string) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if info, ok := b.boxes[sandboxName]; ok {
		return string(info.Status)
	}
	return ""
}

// ReadFile returns the content of path inside sandboxName, or nil if absent.
func (b *Backend) ReadFile(sandboxName, path string) []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if m, ok := b.files[sandboxName]; ok {
		return m[path]
	}
	return nil
}

// ---------------------------------------------------------------------------
// sandbox.Client
// ---------------------------------------------------------------------------

func (b *Backend) ListSandboxes(_ context.Context) ([]sandbox.SandboxInfo, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]sandbox.SandboxInfo, 0, len(b.boxes))
	for _, v := range b.boxes {
		out = append(out, *v)
	}
	return out, nil
}

func (b *Backend) InspectSandbox(_ context.Context, name string) (*sandbox.SandboxInfo, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	info, ok := b.boxes[name]
	if !ok {
		return nil, &sandbox.NotFoundError{Resource: "sandbox " + name}
	}
	cp := *info
	return &cp, nil
}

func (b *Backend) StartSandbox(_ context.Context, name string) (*sandbox.SandboxInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	info, ok := b.boxes[name]
	if !ok {
		return nil, &sandbox.NotFoundError{Resource: "sandbox " + name}
	}
	if !b.NeverRun {
		info.Status = sandboxapi.SandboxInfoStatusRunning
	}
	cp := *info
	return &cp, nil
}

func (b *Backend) StopSandbox(_ context.Context, name string) (*sandbox.SandboxInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	info, ok := b.boxes[name]
	if !ok {
		return nil, &sandbox.NotFoundError{Resource: "sandbox " + name}
	}
	info.Status = sandboxapi.SandboxInfoStatusStopped
	cp := *info
	return &cp, nil
}

func (b *Backend) DeleteSandbox(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.boxes[name]; !ok {
		return &sandbox.NotFoundError{Resource: "sandbox " + name}
	}
	delete(b.boxes, name)
	delete(b.ports, name)
	delete(b.files, name)
	return nil
}

func (b *Backend) ListPublishedPorts(_ context.Context, name string) ([]sandbox.PublishedPort, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]sandbox.PublishedPort(nil), b.ports[name]...), nil
}

func (b *Backend) PublishPorts(_ context.Context, name string, reqs []sandbox.PortPublishRequest) ([]sandbox.PublishedPort, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	added := make([]sandbox.PublishedPort, 0, len(reqs))
	for _, r := range reqs {
		proto := sandboxapi.PublishedPortProtocol("tcp")
		if r.Protocol != nil {
			proto = sandboxapi.PublishedPortProtocol(string(*r.Protocol))
		}
		p := sandbox.PublishedPort{
			HostIp:      "127.0.0.1",
			HostPort:    r.HostPort,
			SandboxPort: r.SandboxPort,
			Protocol:    proto,
		}
		b.ports[name] = append(b.ports[name], p)
		added = append(added, p)
	}
	return added, nil
}

func (b *Backend) UnpublishPorts(_ context.Context, name string, keys []sandbox.PortKey) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	existing := b.ports[name]
	var kept []sandbox.PublishedPort
	for _, p := range existing {
		remove := false
		for _, k := range keys {
			if k.SandboxPort == p.SandboxPort {
				remove = true
				break
			}
		}
		if !remove {
			kept = append(kept, p)
		}
	}
	b.ports[name] = kept
	return nil
}

// ---------------------------------------------------------------------------
// sandbox.SbxAdapter
// ---------------------------------------------------------------------------

func (b *Backend) EnsureDaemon(_ context.Context) error { return nil }

func (b *Backend) Create(_ context.Context, req sandbox.CreateRequest) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.creates[req.Name]++
	if err, ok := b.FailCreate[req.Name]; ok && err != nil {
		return err
	}
	b.boxes[req.Name] = &sandboxapi.SandboxInfo{
		Name:   req.Name,
		Status: sandboxapi.SandboxInfoStatusStopped,
	}
	return nil
}

func (b *Backend) SecretSetCustom(_ context.Context, _ string, _ sandbox.SecretSetRequest) error {
	return nil
}
func (b *Backend) PolicySetDefault(_ context.Context, _ string) error { return nil }
func (b *Backend) PolicyAllow(_ context.Context, _, _ string) error   { return nil }
func (b *Backend) PolicyDeny(_ context.Context, _, _ string) error    { return nil }
func (b *Backend) PolicyRemove(_ context.Context, _, _ string) error  { return nil }
func (b *Backend) List(_ context.Context) ([]sandbox.SbxListEntry, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]sandbox.SbxListEntry, 0, len(b.boxes))
	for _, info := range b.boxes {
		out = append(out, sandbox.SbxListEntry{
			Name:   info.Name,
			Status: string(info.Status),
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// sandbox.SessionDriver
// ---------------------------------------------------------------------------

// AttachAgentSession returns a FakeSession whose behaviour is controlled by
// ConfigureSession. If no session is configured for the sandbox, the session
// succeeds with no output and no emits.
func (b *Backend) AttachAgentSession(_ context.Context, name string, _ sandboxapi.AgentSessionRequest) (sandbox.Session, error) {
	b.mu.RLock()
	cfg := b.sessions[name]
	b.mu.RUnlock()
	if cfg == nil {
		cfg = &SessionConfig{}
	}
	return &FakeSession{backend: b, sandboxName: name, cfg: cfg}, nil
}

// ---------------------------------------------------------------------------
// sandbox.FileStager
// ---------------------------------------------------------------------------

func (b *Backend) GetFile(_ context.Context, name, path string) ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	m, ok := b.files[name]
	if !ok {
		return nil, &sandbox.NotFoundError{Resource: fmt.Sprintf("sandbox %s path %s", name, path)}
	}
	data, ok := m[path]
	if !ok {
		return nil, &sandbox.NotFoundError{Resource: fmt.Sprintf("sandbox %s path %s", name, path)}
	}
	return append([]byte(nil), data...), nil
}

func (b *Backend) PutFile(_ context.Context, name, path string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.putFileLocked(name, path, data)
	return nil
}

// putFileLocked stores data at path inside sandbox name (caller must hold write lock).
func (b *Backend) putFileLocked(name, path string, data []byte) {
	if b.files[name] == nil {
		b.files[name] = make(map[string][]byte)
	}
	b.files[name][path] = append([]byte(nil), data...)
}

// ---------------------------------------------------------------------------
// FakeSession
// ---------------------------------------------------------------------------

// FakeSession implements sandbox.Session for testing.
type FakeSession struct {
	backend     *Backend
	sandboxName string
	cfg         *SessionConfig
}

var _ sandbox.Session = (*FakeSession)(nil)

// ExecID returns a synthetic exec ID based on the sandbox name.
func (s *FakeSession) ExecID() string {
	return "fake-exec-" + strings.ReplaceAll(s.sandboxName, "/", "-")
}

// Stream writes cfg.Output to stdout and stages cfg.Emits payloads into the
// backend's file store (simulating the agent writing .hako/out/<ch>.json
// before exiting), then returns nil.
func (s *FakeSession) Stream(stdout, _ io.Writer) error {
	if s.cfg.Output != "" {
		_, _ = io.WriteString(stdout, s.cfg.Output)
	}
	// Stage emit files so orchestrator emit-detection can read them.
	s.backend.mu.Lock()
	for ch, payload := range s.cfg.Emits {
		path := sandbox.HakoOutPath(ch)
		s.backend.putFileLocked(s.sandboxName, path, []byte(payload))
	}
	s.backend.mu.Unlock()
	return nil
}

// ExitCode returns cfg.ExitCode.
func (s *FakeSession) ExitCode(_ context.Context) (int, error) {
	return s.cfg.ExitCode, nil
}

// Close is a no-op.
func (s *FakeSession) Close() error { return nil }
