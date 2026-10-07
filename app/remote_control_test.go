package app

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRemoteControlStartPlatformBoundary(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"remote-control", "--json", "start"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	// The lifecycle runs on Unix and Windows alike (Rust ensure_supported_platform),
	// and the daemon installs its own package before it starts, so a binary that
	// is not a packaged CLI reports Rust's missing-package error.
	if err == nil || !strings.Contains(err.Error(), "this CLI has no complete local package") {
		t.Fatalf("remote-control start error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

// TestRemoteControlForegroundBindsControlSocketOnWindows pins the Windows
// transport: the foreground command serves its own control socket through the
// codexuds package instead of failing with an unsupported-transport error
// (Rust serves the same socket through the codex-uds crate). A plain
// `remote-control` prefers the managed daemon since #50803, so the foreground
// transport is selected with --no-daemon, exactly as Rust keeps it reachable.
func TestRemoteControlForegroundBindsControlSocketOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("foreground remote-control uses a Unix socket")
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	err := Run(ctx, []string{"remote-control", "--no-daemon"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err != nil && strings.Contains(err.Error(), "unix socket transport is not supported") {
		t.Fatalf("foreground remote control could not serve the control socket: %v", err)
	}
	if !strings.Contains(stdout.String(), "Starting app-server with remote control enabled...") {
		t.Fatalf("stdout = %q, want the foreground progress line", stdout.String())
	}
}

func TestRemoteControlPairHuman(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"remote-control", "pair"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("remote-control pair returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Pairing code:") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRemoteControlStopHuman(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"remote-control", "stop"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("remote-control stop returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Remote control is not running.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
