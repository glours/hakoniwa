package sandbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

// newTestClient creates a DaemonClient pointed at the given test server URL.
func newTestClient(t *testing.T, server *httptest.Server) sandbox.Client {
	t.Helper()
	c, err := sandbox.NewDaemonClientForURL(server.URL)
	if err != nil {
		t.Fatalf("NewDaemonClientForURL: %v", err)
	}
	return c
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("json.Encode: %v", err)
	}
}

var sandboxFixture = sandboxapi.SandboxInfo{Id: "id-1", Name: "proj-agent-a"}

func TestListSandboxes(t *testing.T) {
	infos := []sandboxapi.SandboxInfo{sandboxFixture, {Id: "id-2", Name: "proj-agent-b"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, infos)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(got) != 2 || got[0].Name != "proj-agent-a" {
		t.Errorf("got %v", got)
	}
}

func TestInspectSandboxFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, sandboxFixture)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).InspectSandbox(context.Background(), "proj-agent-a")
	if err != nil {
		t.Fatalf("InspectSandbox: %v", err)
	}
	if got.Name != "proj-agent-a" {
		t.Errorf("Name = %q", got.Name)
	}
}

func TestInspectSandboxNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).InspectSandbox(context.Background(), "no-such")
	if !sandbox.IsNotFound(err) {
		t.Errorf("expected *NotFoundError, got %T: %v", err, err)
	}
}

func TestStartSandbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, sandboxFixture)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).StartSandbox(context.Background(), "proj-agent-a")
	if err != nil {
		t.Fatalf("StartSandbox: %v", err)
	}
	if got.Id != "id-1" {
		t.Errorf("Id = %q", got.Id)
	}
}

func TestStartSandboxNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).StartSandbox(context.Background(), "no-such")
	if !sandbox.IsNotFound(err) {
		t.Errorf("expected *NotFoundError, got %T: %v", err, err)
	}
}

func TestStopSandbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, sandboxFixture)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).StopSandbox(context.Background(), "proj-agent-a")
	if err != nil {
		t.Fatalf("StopSandbox: %v", err)
	}
	if got.Name != "proj-agent-a" {
		t.Errorf("Name = %q", got.Name)
	}
}

func TestStopSandboxNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).StopSandbox(context.Background(), "no-such")
	if !sandbox.IsNotFound(err) {
		t.Errorf("expected *NotFoundError, got %T: %v", err, err)
	}
}

func TestDeleteSandboxSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, sandboxFixture)
	}))
	defer srv.Close()

	if err := newTestClient(t, srv).DeleteSandbox(context.Background(), "proj-agent-a"); err != nil {
		t.Fatalf("DeleteSandbox: %v", err)
	}
}

func TestDeleteSandboxNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := newTestClient(t, srv).DeleteSandbox(context.Background(), "no-such")
	if !sandbox.IsNotFound(err) {
		t.Errorf("expected *NotFoundError, got %T: %v", err, err)
	}
}

func TestListPublishedPorts(t *testing.T) {
	ports := []sandboxapi.PublishedPort{{HostPort: 8080, SandboxPort: 8080}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, ports)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).ListPublishedPorts(context.Background(), "proj-agent-a")
	if err != nil {
		t.Fatalf("ListPublishedPorts: %v", err)
	}
	if len(got) != 1 || got[0].HostPort != 8080 {
		t.Errorf("unexpected ports: %v", got)
	}
}

func TestPublishPorts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ports := []sandbox.PortPublishRequest{{HostPort: 9090, SandboxPort: 9090}}
	if err := newTestClient(t, srv).PublishPorts(context.Background(), "proj-agent-a", ports); err != nil {
		t.Fatalf("PublishPorts: %v", err)
	}
}

func TestPublishPortsBadRequest(t *testing.T) {
	msg := "port 9090 already in use"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, sandboxapi.ErrorResponse{Message: msg})
	}))
	defer srv.Close()

	ports := []sandbox.PortPublishRequest{{HostPort: 9090, SandboxPort: 9090}}
	err := newTestClient(t, srv).PublishPorts(context.Background(), "proj-agent-a", ports)
	if err == nil {
		t.Fatal("expected error for 400, got nil")
	}
	if !strings.Contains(err.Error(), "port 9090 already in use") {
		t.Errorf("error %q does not contain port conflict message", err.Error())
	}
}

func TestUnpublishPorts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hostPort := 9090
	keys := []sandbox.PortKey{{HostPort: &hostPort}}
	if err := newTestClient(t, srv).UnpublishPorts(context.Background(), "proj-agent-a", keys); err != nil {
		t.Fatalf("UnpublishPorts: %v", err)
	}
}

func TestIsNotFoundUnwraps(t *testing.T) {
	inner := &sandbox.NotFoundError{Resource: "sandbox x"}
	wrapped := fmt.Errorf("outer: %w", inner)
	if !sandbox.IsNotFound(wrapped) {
		t.Error("IsNotFound should unwrap errors.As chains")
	}
}

func TestIsNotFoundFalseForOther(t *testing.T) {
	if sandbox.IsNotFound(errors.New("other")) {
		t.Error("IsNotFound should return false for non-NotFoundError")
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
	var _ sandbox.Client = (*sandbox.DaemonClient)(nil)
}
