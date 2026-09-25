package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/execserver"
)

// Rust parity: the exec-server environment provider is loaded during app-server
// startup (EnvironmentManager::from_codex_home), so a malformed
// environments.toml fails startup instead of being ignored.
func TestRunAppServerRejectsMalformedEnvironmentsTOMLLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, execserver.EnvironmentsTOMLFile), []byte("unknown = true\n"), 0o600); err != nil {
		t.Fatalf("write environments.toml: %v", err)
	}
	err := Run(context.Background(), []string{"app-server", "--stdio"}, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil {
		t.Fatal("app-server started with a malformed environments.toml")
	}
	if !strings.Contains(err.Error(), "unknown field `unknown`") {
		t.Fatalf("startup error = %v, want the environments.toml parse failure", err)
	}
	if strings.Contains(err.Error(), "unknown = true") {
		t.Fatalf("startup error echoed the configuration: %v", err)
	}
}

// TestRunAppServerAcceptsConfiguredEnvironmentsLikeRust verifies that a valid
// environments.toml (including a stdio environment) does not block startup.
func TestRunAppServerAcceptsConfiguredEnvironmentsLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	contents := `
default = "none"
include_local = true

[[environments]]
id = "devbox"
url = "wss://executor.example/exec"
auth_bearer_token = "private-token"

[[environments]]
id = "ssh-dev"
program = "ssh"
args = ["dev", "codex exec-server --listen stdio"]
`
	if err := os.WriteFile(filepath.Join(home, execserver.EnvironmentsTOMLFile), []byte(contents), 0o600); err != nil {
		t.Fatalf("write environments.toml: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		// A closed stdin ends the stdio app-server session, so cancel after a
		// short grace period instead.
		done <- Run(ctx, []string{"app-server", "--stdio"}, blockingReader{}, io.Discard, io.Discard)
	}()
	select {
	case err := <-done:
		t.Fatalf("app-server exited during startup: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("app-server did not stop after cancellation")
	}
}

// blockingReader never returns data, keeping a stdio session open until its
// context is cancelled.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) {
	time.Sleep(10 * time.Millisecond)
	return 0, nil
}
