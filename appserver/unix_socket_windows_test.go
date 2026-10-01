//go:build windows

package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/codexuds"
)

// TestServeUnixSocketServesOnWindows pins the Windows control-socket transport:
// the listener secures its directory, binds the AF_UNIX path, and answers a
// request. Rust serves the same socket through the codex-uds crate.
func TestServeUnixSocketServesOnWindows(t *testing.T) {
	// AF_UNIX sun_path is 108 bytes, so the home stays short.
	codexHome, err := os.MkdirTemp("", "cxsock")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	defer os.RemoveAll(codexHome)
	socketPath := filepath.Join(codexHome, "app-server-control", "app-server-control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- ServeUnixSocket(ctx, &UnixSocketOptions{CodexHome: codexHome, Listen: "unix://" + socketPath})
	}()

	conn := dialControlSocketWebSocket(t, socketPath)
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 15*time.Second)
	if err := conn.Write(writeCtx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":1,"method":"unknown"}`)); err != nil {
		cancelWrite()
		t.Fatalf("write to control socket error = %v", err)
	}
	cancelWrite()
	readCtx, cancelRead := context.WithTimeout(context.Background(), 15*time.Second)
	_, data, err := conn.Read(readCtx)
	cancelRead()
	if err != nil {
		t.Fatalf("read from control socket error = %v", err)
	}
	if !strings.Contains(string(data), "\"id\":1") {
		t.Fatalf("control socket response = %q, want a response for request 1", string(data))
	}
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("ServeUnixSocket error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("ServeUnixSocket did not stop after cancellation")
	}
}

// TestPreparePrivateSocketDirectoryLikeRust pins the protected, user-only
// rendezvous directory contract.
func TestPreparePrivateSocketDirectoryLikeRust(t *testing.T) {
	root, err := os.MkdirTemp("", "cxdir")
	if err != nil {
		t.Fatalf("MkdirTemp error = %v", err)
	}
	defer os.RemoveAll(root)
	directory := filepath.Join(root, "app-server-control")
	if err := codexuds.PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatalf("PreparePrivateSocketDirectory() error = %v", err)
	}
	if _, guard, err := codexuds.ValidatePrivateSocketPath(filepath.Join(directory, "app-server-control.sock")); err != nil {
		t.Fatalf("ValidatePrivateSocketPath() error = %v", err)
	} else if guard != nil {
		_ = guard.Close()
	}
	// Preparing the same directory again accepts the contract it already has.
	if err := codexuds.PreparePrivateSocketDirectory(directory); err != nil {
		t.Fatalf("second PreparePrivateSocketDirectory() error = %v", err)
	}
}

// TestServeUnixSocketRejectsLongSocketPathLikeRust pins the AF_UNIX path limit:
// Rust refuses a path longer than sun_path instead of failing inside bind.
func TestServeUnixSocketRejectsLongSocketPathLikeRust(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 90), "app-server-control.sock")
	err := ServeUnixSocket(context.Background(), &UnixSocketOptions{CodexHome: t.TempDir(), Listen: "unix://" + long})
	if err == nil || !strings.Contains(err.Error(), "shorter than SUN_LEN") {
		t.Fatalf("ServeUnixSocket( long path ) error = %v, want the SUN_LEN refusal", err)
	}
}
