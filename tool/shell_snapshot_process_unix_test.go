//go:build unix

package tool

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust #49782 (exec-server/src/shell_snapshot_process.rs): the capture
// leads its own process group, so the cleanup guard can reach whatever the
// user's startup files launched.
func TestSnapshotCaptureOwnsItsProcessGroupLikeRust(t *testing.T) {
	process := osexec.Command("sh", "-c", "exit 0")
	ownSnapshotCaptureProcessGroup(process)
	if process.SysProcAttr == nil || !process.SysProcAttr.Setpgid {
		t.Fatalf("capture SysProcAttr = %#v, want a new process group", process.SysProcAttr)
	}
}

// A cancelled capture takes the helper its startup files left running down
// with it. os/exec stops the leader on its own; the group kill is what reaches
// the helper, which is why cancellation is wired through the group and not
// through Cmd.Cancel (that field only exists for CommandContext commands, and
// setting it on an ordinary Command makes every capture fail).
func TestSnapshotCaptureCancellationKillsStartupHelpersLikeRust(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "helper.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process := osexec.CommandContext(ctx, "sh", "-c", "sleep 300 & echo $! > '"+pidFile+"'; wait")

	done := make(chan error, 1)
	go func() { done <- runSnapshotCaptureProcess(ctx, process) }()

	var helperPID int
	deadline := time.Now().Add(15 * time.Second)
	for helperPID == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if raw, err := os.ReadFile(pidFile); err == nil {
			helperPID, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
	}
	if helperPID <= 0 {
		t.Fatal("the capture never reported its startup helper")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the capture did not return after its context was cancelled")
	}

	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(helperPID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("startup helper %d outlived the cancelled capture", helperPID)
}

// processAlive reports a live, non-zombie process.
func processAlive(pid int) bool {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(raw))
	return len(fields) > 2 && fields[2] != "Z"
}
