package execserver

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

const stdioClientHelperEnv = "CODEX_GO_EXEC_SERVER_STDIO_CLIENT_HELPER"

// TestExecServerStdioClientHelperProcess serves the exec-server protocol over
// stdio in a child process, so the stdio client transport can be exercised
// end-to-end.
func TestExecServerStdioClientHelperProcess(t *testing.T) {
	if os.Getenv(stdioClientHelperEnv) != "1" {
		return
	}
	if err := NewServer().Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func stdioClientHelperCommand(t *testing.T) *StdioExecServerCommand {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	return &StdioExecServerCommand{
		Program: executable,
		Args:    []string{"-test.run=^TestExecServerStdioClientHelperProcess$"},
		Env:     map[string]string{stdioClientHelperEnv: "1"},
	}
}

// Rust parity: codex-exec-server's client_transport::connect_stdio_command and
// the environments.toml `program` entries: a stdio environment is reached by
// spawning the program and speaking JSON-RPC over its stdin/stdout.
func TestStdioClientConnectsAndServesLikeRust(t *testing.T) {
	command := stdioClientHelperCommand(t)
	client, err := DialClientWithOptions(context.Background(), "", DialClientOptions{
		ClientName:   "stdio-client-test",
		StdioCommand: command,
	})
	if err != nil {
		t.Fatalf("DialClientWithOptions(stdio) error = %v", err)
	}
	defer client.Close()
	if client.SessionID() == "" {
		t.Fatal("stdio client session id is empty")
	}

	info, err := client.EnvironmentInfo(context.Background())
	if err != nil {
		t.Fatalf("EnvironmentInfo() error = %v", err)
	}
	if strings.TrimSpace(info.Shell.Name) == "" || strings.TrimSpace(info.Shell.Path) == "" {
		t.Fatalf("EnvironmentInfo() = %#v", info)
	}

	status, err := client.EnvironmentStatus(context.Background())
	if err != nil {
		t.Fatalf("EnvironmentStatus() error = %v", err)
	}
	if status.Status != EnvironmentStatusReady {
		t.Fatalf("status = %q, want ready", status.Status)
	}

	// The child is terminated on close.
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := client.EnvironmentInfo(context.Background()); err == nil {
		t.Fatal("EnvironmentInfo() after Close() succeeded")
	}
}

// TestStdioClientRejectsConflictingTransportLikeRust pins the transport
// exclusivity: a stdio command cannot be combined with a WebSocket URL.
func TestStdioClientRejectsConflictingTransportLikeRust(t *testing.T) {
	_, err := DialClientWithOptions(context.Background(), "ws://127.0.0.1:1/exec", DialClientOptions{
		ClientName:   "stdio-client-test",
		StdioCommand: stdioClientHelperCommand(t),
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with an exec-server url") {
		t.Fatalf("conflicting transport error = %v", err)
	}
}

// TestStdioClientIsNotRecoverableLikeRust pins the stdio transport's missing
// reconnect strategy: Rust registers stdio connections without
// ExecServerReconnectStrategy, so a dropped connection fails instead of
// resuming a session the child cannot know about.
func TestStdioClientIsNotRecoverableLikeRust(t *testing.T) {
	command := stdioClientHelperCommand(t)
	client, err := DialClientWithOptions(context.Background(), "", DialClientOptions{
		ClientName:   "stdio-client-reconnect-test",
		StdioCommand: command,
	})
	if err != nil {
		t.Fatalf("DialClientWithOptions(stdio) error = %v", err)
	}
	defer client.Close()

	// Drop the connection and make sure a subsequent call still succeeds: the
	// recovery path respawns the program.
	client.mu.Lock()
	originalConn := client.conn
	client.mu.Unlock()
	if originalConn == nil {
		t.Fatal("client connection is nil")
	}
	if err := originalConn.CloseNow(); err != nil {
		t.Fatalf("CloseNow() error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		client.mu.Lock()
		disconnected := client.conn == nil
		client.mu.Unlock()
		if disconnected {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	callCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.EnvironmentInfo(callCtx); err == nil {
		t.Fatal("EnvironmentInfo() after a stdio disconnect succeeded, want a non-recoverable failure")
	} else if !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("recovery error = %v, want a disconnected transport error", err)
	}
}
