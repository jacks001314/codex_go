package tool

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/execserver"
)

// TestUnifiedExecEnvironmentFileSystemsLikeRust covers the environment
// filesystem provider that the environment-backed handlers resolve through.
//
// Rust #20647 (`78421face0`, "Route process tools to selected environments")
// gives each handler the selected `TurnEnvironment`'s
// `environment.get_filesystem()` (codex-rs/exec-server/src/environment.rs:1171),
// which is the in-process filesystem for the implicit local environment and an
// exec-server client for a selected remote environment
// (`Environment::is_remote`, :833). The Go provider must make the same
// distinction: `FileSystemFor("remote-x")` reads through the executor, while
// `FileSystemFor("")`/`("local")` read the process filesystem, and an
// environment the turn never resolved is not usable.
//
// The test drives a real in-process exec server over a WebSocket transport, so a
// remote read is a genuine wire call; the same-named file is planted in both
// directories with different contents, so a mis-route to the local filesystem is
// observable rather than silent.
func TestUnifiedExecEnvironmentFileSystemsLikeRust(t *testing.T) {
	serverURL, stop := startEnvironmentFileSystemExecServer(t)
	defer stop()

	remoteDir := t.TempDir()
	localDir := t.TempDir()
	const fileName = "remote-only.txt"
	const remoteContents = "from the remote environment"
	const localContents = "from the local environment"
	if err := os.WriteFile(filepath.Join(remoteDir, fileName), []byte(remoteContents), 0o600); err != nil {
		t.Fatalf("write remote file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDir, fileName), []byte(localContents), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}

	provider := NewUnifiedExecEnvironmentFileSystems([]UnifiedExecEnvironment{{
		ID:            "remote-x",
		CWD:           remoteDir,
		ExecServerURL: serverURL,
	}}, localDir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	remote, ok := provider.FileSystemFor("remote-x")
	if !ok {
		t.Fatalf(`FileSystemFor("remote-x") = not usable, want the selected environment`)
	}
	metadata, err := remote.GetMetadata(ctx, fileName, nil)
	if err != nil {
		t.Fatalf("remote GetMetadata(%q) error = %v", fileName, err)
	}
	if !metadata.IsFile {
		t.Fatalf("remote GetMetadata(%q).IsFile = false, want true", fileName)
	}
	contents, err := remote.ReadFile(ctx, fileName, nil)
	if err != nil {
		t.Fatalf("remote ReadFile(%q) error = %v", fileName, err)
	}
	if string(contents) != remoteContents {
		t.Fatalf("remote ReadFile(%q) = %q, want the executor's contents %q", fileName, contents, remoteContents)
	}

	local, ok := provider.FileSystemFor("")
	if !ok {
		t.Fatalf(`FileSystemFor("") = not usable, want the implicit local environment`)
	}
	localContentsRead, err := local.ReadFile(ctx, fileName, nil)
	if err != nil {
		t.Fatalf("local ReadFile(%q) error = %v", fileName, err)
	}
	if string(localContentsRead) != localContents {
		t.Fatalf("local ReadFile(%q) = %q, want the process filesystem's contents %q", fileName, localContentsRead, localContents)
	}
	if implicitLocal, ok := provider.FileSystemFor(execserver.LocalEnvironmentID); !ok || implicitLocal == nil {
		t.Fatalf("FileSystemFor(%q) = %v, %v, want the local environment", execserver.LocalEnvironmentID, implicitLocal, ok)
	}
	if _, ok := provider.FileSystemFor("never-selected"); ok {
		t.Fatalf(`FileSystemFor("never-selected") = usable, want not usable`)
	}
}

// startEnvironmentFileSystemExecServer runs a real exec server in-process over a
// WebSocket transport and returns its URL (the address the server prints to
// stdout, execserver/transport.go ServeWebSocket).
func startEnvironmentFileSystemExecServer(t *testing.T) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	server := execserver.NewServer()
	reader, writer := io.Pipe()
	go func() {
		_ = server.ServeWebSocket(ctx, "127.0.0.1:0", writer)
		_ = writer.Close()
	}()
	buffered := bufio.NewReader(reader)
	line, err := buffered.ReadString('\n')
	if err != nil {
		cancel()
		_ = reader.Close()
		t.Fatalf("read exec-server listen address: %v", err)
	}
	serverURL := strings.TrimSpace(line)
	if !strings.HasPrefix(serverURL, "ws://") {
		cancel()
		_ = reader.Close()
		t.Fatalf("exec-server listen line = %q, want a ws:// URL", serverURL)
	}
	return serverURL, func() {
		cancel()
		_ = reader.Close()
	}
}
