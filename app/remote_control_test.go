package app

import (
	"bytes"
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/remotecontrol"
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

// TestRemoteControlPairUsesRunningDaemonLikeRust covers Rust #57228's CLI
// contract: `pair` relays remoteControl/pairing/start to the already-running
// daemon over its control socket and prints the manual code, instead of
// starting a pairing session in the CLI process.
func TestRemoteControlPairUsesRunningDaemonLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	restoreRemoteControlPairOnSocket(t)
	var socketPath string
	manual := "ABCD-EFGH"
	remoteControlPairOnSocket = func(path string) (*remotecontrol.PairingStartResponse, error) {
		socketPath = path
		return &remotecontrol.PairingStartResponse{
			PairingCode:       "pairing-code",
			ManualPairingCode: &manual,
			EnvironmentID:     "env-1",
		}, nil
	}

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"remote-control", "pair"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("remote-control pair returned error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "Pairing code: ABCD-EFGH" {
		t.Fatalf("stdout = %q, want the manual pairing code", got)
	}
	want := appserverdaemon.NewLifecycleRunnerForCodexHome(auth.DefaultCodexHome(), "").Daemon.Paths.SocketPath
	if socketPath != want {
		t.Fatalf("pairing socket = %q, want the daemon control socket %q", socketPath, want)
	}
}

// TestRemoteControlPairJSONLikeRust pins the machine-readable branch: --json
// prints the pairing response document and never the human label.
func TestRemoteControlPairJSONLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	restoreRemoteControlPairOnSocket(t)
	manual := "ABCD-EFGH"
	remoteControlPairOnSocket = func(string) (*remotecontrol.PairingStartResponse, error) {
		return &remotecontrol.PairingStartResponse{
			PairingCode:       "pairing-code",
			ManualPairingCode: &manual,
			EnvironmentID:     "env-1",
		}, nil
	}

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"remote-control", "pair", "--json"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("remote-control pair --json returned error: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("stdout is not a JSON document: %v (%q)", err, stdout.String())
	}
	if document["manualPairingCode"] != "ABCD-EFGH" || document["pairingCode"] != "pairing-code" {
		t.Fatalf("pairing document = %#v", document)
	}
}

// TestRemoteControlPairRequiresManualCodeLikeRust pins Rust's human-output
// contract: a response without a manual code is an error.
func TestRemoteControlPairRequiresManualCodeLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	restoreRemoteControlPairOnSocket(t)
	remoteControlPairOnSocket = func(string) (*remotecontrol.PairingStartResponse, error) {
		return &remotecontrol.PairingStartResponse{PairingCode: "pairing-code"}, nil
	}

	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"remote-control", "pair"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "remote-control pairing response did not include a manual pairing code") {
		t.Fatalf("remote-control pair error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

// TestRemoteControlPairRejectsNoDaemonLikeRust pins the clap-level conflict:
// pairing is always served by the daemon, so --no-daemon cannot select it.
func TestRemoteControlPairRejectsNoDaemonLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	restoreRemoteControlPairOnSocket(t)
	remoteControlPairOnSocket = func(string) (*remotecontrol.PairingStartResponse, error) {
		t.Fatal("pairing must not start with --no-daemon")
		return nil, nil
	}

	err := Run(context.Background(), []string{"remote-control", "--no-daemon", "pair"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), remoteControlNoDaemonConflict) {
		t.Fatalf("remote-control --no-daemon pair error = %v", err)
	}
}

// restoreRemoteControlPairOnSocket restores the injectable pairing seam.
func restoreRemoteControlPairOnSocket(t *testing.T) {
	t.Helper()
	original := remoteControlPairOnSocket
	t.Cleanup(func() { remoteControlPairOnSocket = original })
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
