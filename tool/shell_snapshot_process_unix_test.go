//go:build unix

package tool

import (
	osexec "os/exec"
	"testing"
)

// Mirrors Rust #49782 (exec-server/src/shell_snapshot_process.rs): the capture
// leads its own process group and its cancellation reaches that group, so a
// timeout or cancellation takes the startup children down with it.
func TestSnapshotCaptureOwnsItsProcessGroupLikeRust(t *testing.T) {
	process := osexec.Command("sh", "-c", "exit 0")
	ownSnapshotCaptureProcessGroup(process)
	if process.SysProcAttr == nil || !process.SysProcAttr.Setpgid {
		t.Fatalf("capture SysProcAttr = %#v, want a new process group", process.SysProcAttr)
	}
	if process.Cancel == nil {
		t.Fatal("capture cancellation does not reach the process group")
	}
}
