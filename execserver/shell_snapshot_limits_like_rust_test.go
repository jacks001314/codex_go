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
	if err := validateShellSnapshotSizes(&shell.CapturedSnapshot{}, strings.Repeat("x", maxFileShellSnapshotBytes), true); err != nil {
		t.Fatalf("state at the limit was rejected: %v", err)
	}
	err := validateShellSnapshotSizes(&shell.CapturedSnapshot{}, strings.Repeat("x", maxFileShellSnapshotBytes+1), true)
	if err == nil || !strings.Contains(err.Error(), "shell snapshot state exceeds") {
		t.Fatalf("oversized state error = %v", err)
	}
	oversizedEnv := &shell.CapturedSnapshot{Environment: bytes.Repeat([]byte("x"), maxShellSnapshotEnvironmentBytes+1)}
	err = validateShellSnapshotSizes(oversizedEnv, "state", true)
	if err == nil || !strings.Contains(err.Error(), "shell snapshot environment exceeds") {
		t.Fatalf("oversized environment error = %v", err)
	}
	startupEnv := &shell.CapturedSnapshot{StartupEnvironment: &shell.CapturedStartupEnvironment{
		Environment: bytes.Repeat([]byte("x"), maxShellSnapshotEnvironmentBytes+1),
	}}
	if err := validateShellSnapshotSizes(startupEnv, "state", true); err == nil {
		t.Fatal("an oversized startup environment was accepted")
	}
}

// Environment replay shares the 512 KiB budget between captured state and the
// environment, while file replay keeps the environment cap independent.
func TestShellSnapshotEnvironmentReplaySharesTheBudgetLikeRust(t *testing.T) {
	captured := &shell.CapturedSnapshot{Environment: bytes.Repeat([]byte("x"), maxShellSnapshotEnvironmentBytes/2)}
	if err := validateShellSnapshotSizes(captured, strings.Repeat("x", maxShellSnapshotEnvironmentBytes/2), false); err != nil {
		t.Fatalf("state at the environment-replay limit was rejected: %v", err)
	}
	err := validateShellSnapshotSizes(captured, strings.Repeat("x", maxShellSnapshotEnvironmentBytes/2+1), false)
	if err == nil || !strings.Contains(err.Error(), "shell snapshot state exceeds") {
		t.Fatalf("oversized environment-replay state error = %v", err)
	}
	if err := validateShellSnapshotSizes(captured, strings.Repeat("x", maxShellSnapshotEnvironmentBytes/2+1), true); err != nil {
		t.Fatalf("file replay should not share the environment budget: %v", err)
	}
}
