package gitutil

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunCollectsGitOutput(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "test@example.test")
	runGit(t, repo, "config", "user.name", "Test")
	out, err := Run(context.Background(), repo, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out != "true" {
		t.Fatalf("Run() = %q, want true", out)
	}
}

func TestRunReturnsErrorForMissingRepository(t *testing.T) {
	_, err := Run(context.Background(), t.TempDir(), "rev-parse", "--is-inside-work-tree")
	if err == nil {
		t.Fatal("Run() succeeded outside a git repository")
	}
}

func TestRunWithTimeoutKillsProcessTree(t *testing.T) {
	fakeBin := t.TempDir()
	script := "#!/bin/sh\nsleep 30\n"
	name := "git"
	if runtime.GOOS == "windows" {
		script = "@ping 127.0.0.1 -n 30 > nul\n"
		name = "git.cmd"
	}
	if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := os.Getenv("PATH")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+previous)

	start := time.Now()
	_, err := RunWithTimeout(context.Background(), 300*time.Millisecond, t.TempDir(), "rev-parse", "HEAD")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("RunWithTimeout() error = %v, want timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %v, process tree was not terminated", elapsed)
	}
}

func TestRunHonorsContextCancellation(t *testing.T) {
	fakeBin := t.TempDir()
	script := "#!/bin/sh\nsleep 30\n"
	name := "git"
	if runtime.GOOS == "windows" {
		script = "@ping 127.0.0.1 -n 30 > nul\n"
		name = "git.cmd"
	}
	if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := os.Getenv("PATH")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+previous)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := RunWithTimeout(ctx, 30*time.Second, t.TempDir(), "rev-parse", "HEAD")
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil || err != context.Canceled {
			t.Fatalf("RunWithTimeout() error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled run did not return")
	}
}

func runGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	out, err := Run(context.Background(), cwd, args...)
	if err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}
