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

// fakeSbx writes a shell script at dir/sbx that prints a predefined JSON
// response and exits with the given code. Returns the full path to the script.
func fakeSbx(t *testing.T, dir string, exitCode int, stdout string) string {
	t.Helper()
	path := filepath.Join(dir, "sbx")
	script := "#!/bin/sh\n"
	if stdout != "" {
		script += fmt.Sprintf("echo '%s'\n", stdout)
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
	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %s
exit 0
`, captureFile)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sbx capture: %v", err)
	}
	return path
}

// newFakeClient returns a sandbox.Client backed by an httptest server that
// returns the given sandbox info with HTTP 200.
func newFakeClient(t *testing.T, info *sandboxapi.SandboxInfo) sandbox.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if info == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(info)
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
	// daemon status exits 1 (not running), daemon start exits 0
	path := filepath.Join(dir, "sbx")
	calls := filepath.Join(dir, "calls.txt")
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
	err := adapter.Create(context.Background(), sandbox.CreateRequest{
		Name: "proj-agent", Agent: "claude",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestSbxAdapterCreateReuse409(t *testing.T) {
	// sbx create returns exit 1 (conflict), but inspect returns the sandbox.
	dir := t.TempDir()
	sbxPath := fakeSbx(t, dir, 1, "") // create fails
	info := &sandboxapi.SandboxInfo{Id: "id-1", Name: "proj-agent"}
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, info))
	// Should return nil because InspectSandbox succeeds.
	if err := adapter.Create(context.Background(), sandbox.CreateRequest{
		Name: "proj-agent", Agent: "claude",
	}); err != nil {
		t.Fatalf("Create with reuse should be nil, got: %v", err)
	}
}

func TestSbxAdapterCreateFailure(t *testing.T) {
	// sbx create returns exit 1 AND inspect fails (sandbox truly absent).
	dir := t.TempDir()
	sbxPath := fakeSbx(t, dir, 1, "")                                          // create fails
	adapter := sandbox.NewSbxCLIAdapterForTest(sbxPath, newFakeClient(t, nil)) // inspect returns 404
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

func TestSbxAdapterInterface(t *testing.T) {
	var _ sandbox.SbxAdapter = (*sandbox.SbxCLIAdapter)(nil)
}
