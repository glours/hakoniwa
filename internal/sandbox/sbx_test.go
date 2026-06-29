package sandbox_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// fakeSbx writes a shell script at dir/sbx that prints stdout (via printf to
// avoid single-quote issues) and exits with the given code.
func fakeSbx(t *testing.T, dir string, exitCode int, stdout string) string {
	t.Helper()
	path := filepath.Join(dir, "sbx")
	// Use printf with a heredoc-style temp file to avoid quoting issues.
	script := "#!/bin/sh\n"
	if stdout != "" {
		// Write output to a temp file, then cat it, to avoid shell escaping.
		outFile := filepath.Join(dir, "fakeSbxOut")
		if err := os.WriteFile(outFile, []byte(stdout+"\n"), 0o644); err != nil {
			t.Fatalf("write fakeSbxOut: %v", err)
		}
		script += fmt.Sprintf("cat %s\n", outFile)
	}
	script += fmt.Sprintf("exit %d\n", exitCode)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sbx: %v", err)
	}
	return path
}

// fakeSbxCapture writes a script that records its arguments to a file and exits 0.
func fakeSbxCapture(t *testing.T, dir, captureFile string) string {
	t.Helper()
	path := filepath.Join(dir, "sbx")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %s\nexit 0\n", captureFile)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sbx capture: %v", err)
	}
	return path
}

// newFakeClient returns a sandbox.Client backed by an httptest server that
// returns the given sandbox info with HTTP 200, or 404 if info is nil.
func newFakeClient(t *testing.T, info *sandboxapi.SandboxInfo) sandbox.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if info == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(info); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := sandbox.NewDaemonClientForURL(srv.URL)
	if err != nil {
		t.Fatalf("NewDaemonClientForURL: %v", err)
	}
	return c
}

func TestSbxAdapterEnsureDaemonRunning(t *testing.T) {
	dir := t.TempDir()
	sbxPath := fakeSbx(t, dir, 0, "") // daemon status exits 0 = running
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil))
	if err := adapter.EnsureDaemon(context.Background()); err != nil {
		t.Fatalf("EnsureDaemon: %v", err)
	}
}

func TestSbxAdapterEnsureDaemonStarted(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls.txt")
	path := filepath.Join(dir, "sbx")
	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %s
case "$*" in
  "daemon status") exit 1 ;;
  *) exit 0 ;;
esac
`, calls)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := sandbox.NewSbxCLIAdapterForTest(path, newFakeClient(t, nil))
	if err := adapter.EnsureDaemon(context.Background()); err != nil {
		t.Fatalf("EnsureDaemon should start daemon: %v", err)
	}
}

func TestSbxAdapterCreateNew(t *testing.T) {
	dir := t.TempDir()
	sbxPath := fakeSbx(t, dir, 0, "") // create succeeds
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil))
	if err := adapter.Create(context.Background(), sandbox.CreateRequest{
		Name: "proj-agent", Agent: "claude",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestSbxAdapterCreateReuse409(t *testing.T) {
	dir := t.TempDir()
	sbxPath := fakeSbx(t, dir, 1, "") // create fails
	info := &sandboxapi.SandboxInfo{Id: "id-1", Name: "proj-agent"}
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, info))
	if err := adapter.Create(context.Background(), sandbox.CreateRequest{
		Name: "proj-agent", Agent: "claude",
	}); err != nil {
		t.Fatalf("Create with reuse should be nil, got: %v", err)
	}
}

func TestSbxAdapterCreateFailure(t *testing.T) {
	dir := t.TempDir()
	sbxPath := fakeSbx(t, dir, 1, "")                                          // create fails
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil)) // inspect → 404
	err := adapter.Create(context.Background(), sandbox.CreateRequest{
		Name: "proj-agent", Agent: "claude",
	})
	if err == nil {
		t.Fatal("expected error when create fails and sandbox absent")
	}
}

func TestSbxAdapterList(t *testing.T) {
	dir := t.TempDir()
	entries := []sandbox.SbxListEntry{{Name: "a", ID: "id-a", Status: "running"}}
	data, _ := json.Marshal(entries)
	sbxPath := fakeSbx(t, dir, 0, string(data))
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil))
	got, err := adapter.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "a" {
		t.Errorf("List = %v", got)
	}
}

func TestSbxAdapterListEmpty(t *testing.T) {
	// sbx ls --json producing no output should return empty slice, not an error.
	dir := t.TempDir()
	sbxPath := fakeSbx(t, dir, 0, "") // no output
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil))
	got, err := adapter.List(context.Background())
	if err != nil {
		t.Fatalf("List(empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestSbxAdapterSecretSetCustom(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls.txt")
	sbxPath := fakeSbxCapture(t, dir, capture)
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil))
	err := adapter.SecretSetCustom(context.Background(), "my-sandbox", sandbox.SecretSetRequest{
		Value: "tok123",
		Env:   "GH_TOKEN",
		Host:  "api.github.com",
	})
	if err != nil {
		t.Fatalf("SecretSetCustom: %v", err)
	}
	callsBytes, _ := os.ReadFile(capture)
	calls := string(callsBytes)
	for _, want := range []string{"secret", "set-custom", "my-sandbox", "--env", "GH_TOKEN", "--host", "api.github.com", "--value", "tok123"} {
		if !strings.Contains(calls, want) {
			t.Errorf("calls %q missing %q", calls, want)
		}
	}
}

func TestSbxAdapterSecretRedactedInError(t *testing.T) {
	// When sbx secret set-custom fails, the error must NOT contain the secret value.
	dir := t.TempDir()
	sbxPath := fakeSbx(t, dir, 1, "") // command fails
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil))
	err := adapter.SecretSetCustom(context.Background(), "my-sandbox", sandbox.SecretSetRequest{
		Value: "supersecret",
		Env:   "TOKEN",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Errorf("error message leaks secret value: %v", err)
	}
}

func TestSbxAdapterPolicySetDefault(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls.txt")
	sbxPath := fakeSbxCapture(t, dir, capture)
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil))
	if err := adapter.PolicySetDefault(context.Background(), "balanced"); err != nil {
		t.Fatalf("PolicySetDefault: %v", err)
	}
	calls, _ := os.ReadFile(capture)
	for _, want := range []string{"policy", "set-default", "balanced"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("calls %q missing %q", calls, want)
		}
	}
}

func TestSbxAdapterPolicyAllow(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls.txt")
	sbxPath := fakeSbxCapture(t, dir, capture)
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil))
	if err := adapter.PolicyAllow(context.Background(), "proj-agent", "*.github.com"); err != nil {
		t.Fatalf("PolicyAllow: %v", err)
	}
	calls, _ := os.ReadFile(capture)
	for _, want := range []string{"policy", "allow", "--sandbox", "proj-agent", "*.github.com"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("calls %q missing %q", calls, want)
		}
	}
}

func TestSbxAdapterPolicyDeny(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls.txt")
	sbxPath := fakeSbxCapture(t, dir, capture)
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil))
	if err := adapter.PolicyDeny(context.Background(), "proj-agent", "*.telemetry.io"); err != nil {
		t.Fatalf("PolicyDeny: %v", err)
	}
	calls, _ := os.ReadFile(capture)
	for _, want := range []string{"policy", "deny", "--sandbox", "proj-agent", "*.telemetry.io"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("calls %q missing %q", calls, want)
		}
	}
}

func TestSbxAdapterPolicyRemove(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls.txt")
	sbxPath := fakeSbxCapture(t, dir, capture)
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil))
	if err := adapter.PolicyRemove(context.Background(), "proj-agent", "rule-uuid-123"); err != nil {
		t.Fatalf("PolicyRemove: %v", err)
	}
	calls, _ := os.ReadFile(capture)
	for _, want := range []string{"policy", "rm", "--sandbox", "proj-agent", "rule-uuid-123"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("calls %q missing %q", calls, want)
		}
	}
}

func TestSbxAdapterInterface(t *testing.T) {
	var _ sandbox.SbxAdapter = (*sandbox.SbxCLIAdapter)(nil)
}
