package appserver

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Rust thread/shellCommand runs a user shell command outside the sandbox and
// reports the commandExecution item. Embedded Go TUI hosts have no app-server
// session, so RunLocalUserShellCommand mirrors the same standalone task.
func TestRunLocalUserShellCommandLikeRust(t *testing.T) {
	var start LocalUserShellCommandStart
	result := RunLocalUserShellCommand(context.Background(), "thread-shell", "echo hello", t.TempDir(), time.Minute, func(item LocalUserShellCommandStart) {
		start = item
	})
	if start.ItemID == "" || start.ProcessID == "" || start.Command != "echo hello" {
		t.Fatalf("start item = %#v", start)
	}
	if result == nil || result.ItemID != start.ItemID {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Fatalf("output = %q, want hello", result.Output)
	}
	if result.ExitCode != 0 || result.Status != CommandExecutionCompleted {
		t.Fatalf("exit=%d status=%q", result.ExitCode, result.Status)
	}
}

func TestRunLocalUserShellCommandReportsFailureLikeRust(t *testing.T) {
	result := RunLocalUserShellCommand(context.Background(), "thread-shell", "exit 3", t.TempDir(), time.Minute, nil)
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.ExitCode != 3 || result.Status != CommandExecutionFailed {
		t.Fatalf("exit=%d status=%q, want 3/failed", result.ExitCode, result.Status)
	}
}
