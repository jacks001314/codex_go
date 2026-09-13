package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/appserverdaemon"
	"codex_go/worktree"
)

func TestInteractiveStartAgentsDaemonPlatformGate(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	err := interactiveStartAgentsDaemon()
	if err == nil || !strings.Contains(err.Error(), "managed standalone Codex install not found") {
		t.Fatalf("non-Windows start-daemon error = %v", err)
	}
}

func TestInteractiveRemoteAgentsOverviewHandlersWireRemoteSource(t *testing.T) {
	// The handlers must be non-nil and accept the remote endpoint; the row
	// building itself is covered by TestAgentsOverviewRowsFromThreadsLikeRust.
	endpoint := appserverdaemon.NewWebSocketEndpoint("ws://127.0.0.1:1", nil)
	ctx := context.Background()
	refresh := interactiveRemoteAgentsOverviewRefresh(ctx, endpoint)
	newSession := interactiveRemoteAgentsOverviewNewSession(ctx, endpoint)
	stop := interactiveRemoteAgentsOverviewStop(ctx, endpoint)
	rename := interactiveRemoteAgentsOverviewRename(ctx, endpoint)
	if refresh == nil || newSession == nil || stop == nil || rename == nil {
		t.Fatal("a remote agents-overview handler is nil")
	}
	// Calling them against an unreachable endpoint must return a connection
	// error, not panic (the dashboard surfaces it as a notice).
	if _, err := refresh(""); err == nil {
		t.Fatal("refresh against unreachable endpoint succeeded, want error")
	}
	if _, err := newSession(""); err == nil {
		t.Fatal("new session against unreachable endpoint succeeded, want error")
	}
	if err := stop("thread-1"); err == nil {
		t.Fatal("stop against unreachable endpoint succeeded, want error")
	}
	if err := rename("thread-1", "name"); err == nil {
		t.Fatal("rename against unreachable endpoint succeeded, want error")
	}
}

// Rust #45276: a worktree whose session never started is removed when it is
// clean, and reported by path when it cannot be removed.
func TestRetainedWorktreeSessionErrorLikeRust(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	settings, err := worktree.FromDesktopConfig(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("FromDesktopConfig() error = %v", err)
	}
	manager := worktree.NewWorktreeManager(settings)
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s", args, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	for _, args := range [][]string{{"add", "file.txt"}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %s", args, output)
		}
	}

	clean, err := manager.Create(repo, "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	cleaned := retainedWorktreeSessionError(manager, clean, errors.New("start failed"))
	if !strings.Contains(cleaned.Error(), "start failed") || strings.Contains(cleaned.Error(), "A checkout was retained") {
		t.Fatalf("clean checkout error = %v", cleaned)
	}
	if _, statErr := os.Stat(clean.Root); !os.IsNotExist(statErr) {
		t.Fatalf("clean checkout survived: %v", statErr)
	}

	dirty, err := manager.Create(repo, "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirty.CWD, "dirty.txt"), []byte("local work\n"), 0o600); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	retained := retainedWorktreeSessionError(manager, dirty, errors.New("start failed"))
	if !strings.Contains(retained.Error(), "A checkout was retained at "+dirty.Root) {
		t.Fatalf("dirty checkout error = %v", retained)
	}
	if _, statErr := os.Stat(dirty.Root); statErr != nil {
		t.Fatalf("dirty checkout was removed: %v", statErr)
	}
}
