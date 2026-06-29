package sandbox_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// newTestClient creates a DaemonClient pointed at the given test server URL.
// It uses NewDaemonClientForURL which accepts an arbitrary HTTP base URL so
// tests don't need a real Unix socket.
func newTestClient(t *testing.T, server *httptest.Server) sandbox.Client {
	t.Helper()
	c, err := sandbox.NewDaemonClientForURL(server.URL)
	if err != nil {
		t.Fatalf("NewDaemonClientForURL: %v", err)
	}
	return c
}

func TestListSandboxes(t *testing.T) {
	infos := []sandboxapi.SandboxInfo{
		{Id: "id-1", Name: "proj-agent-a"},
		{Id: "id-2", Name: "proj-agent-b"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sandbox" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(infos)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	got, err := client.ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d sandboxes, want 2", len(got))
	}
	if got[0].Name != "proj-agent-a" {
		t.Errorf("got[0].Name = %q, want proj-agent-a", got[0].Name)
	}
}

func TestInspectSandboxFound(t *testing.T) {
	info := sandboxapi.SandboxInfo{Id: "id-1", Name: "proj-agent-a"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(info)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	got, err := client.InspectSandbox(context.Background(), "proj-agent-a")
	if err != nil {
		t.Fatalf("InspectSandbox: %v", err)
	}
	if got.Name != "proj-agent-a" {
		t.Errorf("Name = %q, want proj-agent-a", got.Name)
	}
}

func TestInspectSandboxNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	_, err := client.InspectSandbox(context.Background(), "no-such-sandbox")
	if err == nil {
		t.Fatal("expected NotFoundError, got nil")
	}
	if !sandbox.IsNotFound(err) {
		t.Errorf("expected *NotFoundError, got %T: %v", err, err)
	}
}

func TestDeleteSandboxNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	err := client.DeleteSandbox(context.Background(), "no-such-sandbox")
	if !sandbox.IsNotFound(err) {
		t.Errorf("expected *NotFoundError, got %T: %v", err, err)
	}
}

func TestNotFoundErrorString(t *testing.T) {
	err := &sandbox.NotFoundError{Resource: "sandbox my-proj-agent"}
	want := "sandbox my-proj-agent: not found"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestClientInterface(t *testing.T) {
	// Compile-time check: *DaemonClient must implement Client.
	var _ sandbox.Client = (*sandbox.DaemonClient)(nil)
}
