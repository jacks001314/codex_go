package execserver

import (
	"bytes"
	"strings"
	"testing"

	"codex_go/shell"
)

// Rust #51350: file replay allows 4 MiB of state and 512 KiB of captured
// environment, with distinct failure messages.
func TestShellSnapshotFileReplayLimitsLikeRust(t *testing.T) {
	if err := validateShellSnapshotSizes(&shell.CapturedSnapshot{}, strings.Repeat("x", maxFileShellSnapshotBytes)); err != nil {
		t.Fatalf("state at the limit was rejected: %v", err)
	}
	err := validateShellSnapshotSizes(&shell.CapturedSnapshot{}, strings.Repeat("x", maxFileShellSnapshotBytes+1))
	if err == nil || !strings.Contains(err.Error(), "shell snapshot state exceeds") {
		t.Fatalf("oversized state error = %v", err)
	}
	oversizedEnv := &shell.CapturedSnapshot{Environment: bytes.Repeat([]byte("x"), maxShellSnapshotEnvironmentBytes+1)}
	err = validateShellSnapshotSizes(oversizedEnv, "state")
	if err == nil || !strings.Contains(err.Error(), "shell snapshot environment exceeds") {
		t.Fatalf("oversized environment error = %v", err)
	}
	startupEnv := &shell.CapturedSnapshot{StartupEnvironment: &shell.CapturedStartupEnvironment{
		Environment: bytes.Repeat([]byte("x"), maxShellSnapshotEnvironmentBytes+1),
	}}
	if err := validateShellSnapshotSizes(startupEnv, "state"); err == nil {
		t.Fatal("an oversized startup environment was accepted")
	}
}
