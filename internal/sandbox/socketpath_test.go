package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveSocketPathEnvOverride(t *testing.T) {
	t.Setenv("DOCKER_SANDBOXES_API", "/custom/path/sandboxd.sock")
	got := resolveSocketPath()
	if got != "/custom/path/sandboxd.sock" {
		t.Errorf("resolveSocketPath() = %q, want custom path", got)
	}
}

func TestResolveSocketPathEnvEmpty(t *testing.T) {
	t.Setenv("DOCKER_SANDBOXES_API", "")
	got := resolveSocketPath()
	if got == "" {
		t.Fatal("resolveSocketPath() returned empty string")
	}
	// Must end with sandboxd.sock (the socket filename on all platforms).
	if !strings.HasSuffix(got, "sandboxd.sock") {
		t.Errorf("resolveSocketPath() = %q, expected suffix sandboxd.sock", got)
	}
}

func TestResolveSocketPathDarwinPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-specific test")
	}
	t.Setenv("DOCKER_SANDBOXES_API", "")

	got := resolveSocketPath()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	want := filepath.Join(home, "Library", "Application Support",
		"com.docker.sandboxes", "sandboxes", "sandboxd", "sandboxd.sock")
	if got != want {
		t.Errorf("resolveSocketPath() = %q, want %q", got, want)
	}
}

func TestResolveSocketPathLinuxFallback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-specific test")
	}
	t.Setenv("DOCKER_SANDBOXES_API", "")
	got := resolveSocketPath()
	if got != defaultLinuxSocketPath {
		t.Errorf("resolveSocketPath() = %q, want %q", got, defaultLinuxSocketPath)
	}
}

func TestResolveSocketPathMockedHome(t *testing.T) {
	// Simulate macOS path detection by temporarily overriding HOME on linux.
	// Since runtime.GOOS is linux in CI, we can't actually exercise the darwin
	// branch; the darwin-specific test above covers it on darwin. This test
	// documents the expected path format when home dir is available.
	if runtime.GOOS != "darwin" {
		t.Skip("darwin path format tested only on darwin")
	}
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("DOCKER_SANDBOXES_API", "")

	got := resolveSocketPath()
	want := filepath.Join(tmpHome, "Library", "Application Support",
		"com.docker.sandboxes", "sandboxes", "sandboxd", "sandboxd.sock")
	if got != want {
		t.Errorf("resolveSocketPath() with mocked HOME = %q, want %q", got, want)
	}
}
