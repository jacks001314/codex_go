package tool

import (
	"fmt"
	osexec "os/exec"
	"runtime"
	"testing"
)

// snapshotCaptureExitCommand builds a capture that finishes immediately with
// the given exit code, standing in for a shell startup profile that fails.
func snapshotCaptureExitCommand(code int) *osexec.Cmd {
	if runtime.GOOS == "windows" {
		return osexec.Command("cmd", "/c", fmt.Sprintf("exit %d", code))
	}
	return osexec.Command("sh", "-c", fmt.Sprintf("exit %d", code))
}

// Mirrors Rust #49782 (exec-server/tests/exec_process.rs
// `shell_snapshot_v2_capture_failure_falls_back_and_retries`): a capture that
// fails takes the process group its startup files spawned down with it.
func TestSnapshotCaptureCleansUpFailedCaptureProcessGroupLikeRust(t *testing.T) {
	cleanups := 0
	previous := snapshotCaptureGroupKiller
	t.Cleanup(func() { snapshotCaptureGroupKiller = previous })
	snapshotCaptureGroupKiller = func(*osexec.Cmd) error {
		cleanups++
		return nil
	}

	process := snapshotCaptureExitCommand(7)
	if err := runSnapshotCaptureProcess(process); err == nil {
		t.Fatal("capture of a failing profile reported success")
	}
	if cleanups == 0 {
		t.Fatal("failed capture left its process group running")
	}
}

// Mirrors Rust #49782 (exec-server/tests/exec_process/shell_snapshot.rs
// `shell_snapshot_preserves_successful_startup_output_and_services`): a
// successful capture keeps the helpers its startup files left running.
func TestSnapshotCapturePreservesSuccessfulStartupHelpersLikeRust(t *testing.T) {
	cleanups := 0
	previous := snapshotCaptureGroupKiller
	t.Cleanup(func() { snapshotCaptureGroupKiller = previous })
	snapshotCaptureGroupKiller = func(*osexec.Cmd) error {
		cleanups++
		return nil
	}

	process := snapshotCaptureExitCommand(0)
	if err := runSnapshotCaptureProcess(process); err != nil {
		t.Fatalf("capture error = %v", err)
	}
	if cleanups != 0 {
		t.Fatalf("successful capture cleaned up its helpers %d times", cleanups)
	}
}
