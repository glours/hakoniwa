package sandbox

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers for files endpoint test server
// ---------------------------------------------------------------------------

// filesServer builds a test server that handles GET and PUT /sandbox/{name}/files.
// It stores the last PUT body (tar) for inspection and serves a configurable
// GET response.
type filesServer struct {
	t *testing.T
	// GET config
	getStatus   int    // default 200
	getContents []byte // file bytes to serve in tar form
	// PUT tracking
	lastPutDir  string
	lastPutBody []byte
	// Error overrides
	errStatus  int
	errMessage string
}

func (fs *filesServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/sandbox/")
	name = strings.TrimSuffix(name, "/files")

	if fs.errStatus != 0 {
		body, _ := json.Marshal(map[string]string{"message": fs.errMessage})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(fs.errStatus)
		_, _ = w.Write(body)
		return
	}

	switch r.Method {
	case http.MethodGet:
		path := r.URL.Query().Get("path")
		fs.t.Logf("GET sandbox=%q path=%q", name, path)
		status := fs.getStatus
		if status == 0 {
			status = http.StatusOK
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		// Return file as tar.
		tarBytes, err := buildTarEntry("out.json", fs.getContents)
		if err != nil {
			fs.t.Fatalf("build tar: %v", err)
		}
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = w.Write(tarBytes)

	case http.MethodPut:
		fs.lastPutDir = r.URL.Query().Get("path")
		fs.t.Logf("PUT sandbox=%q path=%q", name, fs.lastPutDir)
		fs.lastPutBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"message":"ok"}`)
	}
}

func newFilesTestClient(t *testing.T, fs *filesServer) (*DaemonClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(fs)
	t.Cleanup(srv.Close)
	c, err := NewDaemonClientForURL(srv.URL)
	if err != nil {
		t.Fatalf("NewDaemonClientForURL: %v", err)
	}
	return c, srv
}

// ---------------------------------------------------------------------------
// GetFile tests
// ---------------------------------------------------------------------------

func TestGetFileReturnsContent(t *testing.T) {
	want := []byte(`{"status":"done"}`)
	fs := &filesServer{t: t, getContents: want}
	client, _ := newFilesTestClient(t, fs)

	got, err := client.GetFile(t.Context(), "my-sandbox", "/root/.hako/out/repro.ready.json")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGetFileNotFound(t *testing.T) {
	fs := &filesServer{t: t, errStatus: http.StatusNotFound, errMessage: "not found"}
	client, _ := newFilesTestClient(t, fs)

	_, err := client.GetFile(t.Context(), "sb", "/root/.hako/out/x.json")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsNotFound(err) {
		t.Errorf("expected NotFoundError, got: %v", err)
	}
}

func TestGetFileServerError(t *testing.T) {
	fs := &filesServer{t: t, errStatus: http.StatusConflict, errMessage: "not running"}
	client, _ := newFilesTestClient(t, fs)

	_, err := client.GetFile(t.Context(), "sb", "/root/.hako/out/x.json")
	if err == nil {
		t.Fatal("expected error for 409")
	}
	if !strings.Contains(err.Error(), "409") {
		t.Errorf("expected '409' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// PutFile tests
// ---------------------------------------------------------------------------

func TestPutFileUploadsAsTar(t *testing.T) {
	fs := &filesServer{t: t}
	client, _ := newFilesTestClient(t, fs)

	data := []byte(`{"input":"hello"}`)
	if err := client.PutFile(t.Context(), "sb", "/root/.hako/in/repro.ready.json", data); err != nil {
		t.Fatalf("PutFile: %v", err)
	}

	// The path query param should be the directory.
	if fs.lastPutDir != "/root/.hako/in" {
		t.Errorf("PUT path = %q, want %q", fs.lastPutDir, "/root/.hako/in")
	}

	// The body should be a tar archive containing the file.
	tr := tar.NewReader(bytes.NewReader(fs.lastPutBody))
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("read tar header: %v", err)
	}
	if hdr.Name != "repro.ready.json" {
		t.Errorf("tar entry name = %q, want %q", hdr.Name, "repro.ready.json")
	}
	content, _ := io.ReadAll(tr)
	if !bytes.Equal(content, data) {
		t.Errorf("tar content = %q, want %q", content, data)
	}
}

func TestPutFileNotFound(t *testing.T) {
	fs := &filesServer{t: t, errStatus: http.StatusNotFound}
	client, _ := newFilesTestClient(t, fs)

	err := client.PutFile(t.Context(), "missing", "/a/b/c.json", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsNotFound(err) {
		t.Errorf("expected NotFoundError, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Convenience helpers: ReadChannelPayload / StageChannelPayload
// ---------------------------------------------------------------------------

func TestReadChannelPayload(t *testing.T) {
	want := json.RawMessage(`{"out":"value"}`)
	fs := &filesServer{t: t, getContents: want}
	client, _ := newFilesTestClient(t, fs)

	got, err := client.ReadChannelPayload(t.Context(), "sb", "repro.ready")
	if err != nil {
		t.Fatalf("ReadChannelPayload: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("payload = %q, want %q", got, want)
	}
}

func TestStageChannelPayload(t *testing.T) {
	fs := &filesServer{t: t}
	client, _ := newFilesTestClient(t, fs)

	payload := json.RawMessage(`{"in":"data"}`)
	if err := client.StageChannelPayload(t.Context(), "sb", "fix.ready", payload); err != nil {
		t.Fatalf("StageChannelPayload: %v", err)
	}

	// Verify the put landed at the right directory path.
	wantDir := "/root/.hako/in"
	if fs.lastPutDir != wantDir {
		t.Errorf("PUT path = %q, want %q", fs.lastPutDir, wantDir)
	}
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

func TestHakoOutPath(t *testing.T) {
	got := HakoOutPath("repro.ready")
	if got != "/root/.hako/out/repro.ready.json" {
		t.Errorf("HakoOutPath = %q", got)
	}
}

func TestHakoInPath(t *testing.T) {
	got := HakoInPath("fix.ready")
	if got != "/root/.hako/in/fix.ready.json" {
		t.Errorf("HakoInPath = %q", got)
	}
}

func TestSplitPath(t *testing.T) {
	cases := []struct{ path, dir, base string }{
		{"/root/.hako/in/c.json", "/root/.hako/in", "c.json"},
		{"/a/b", "/a", "b"},
		{"nodir", ".", "nodir"},
	}
	for _, tc := range cases {
		d, b := splitPath(tc.path)
		if d != tc.dir || b != tc.base {
			t.Errorf("splitPath(%q) = %q, %q; want %q, %q", tc.path, d, b, tc.dir, tc.base)
		}
	}
}
