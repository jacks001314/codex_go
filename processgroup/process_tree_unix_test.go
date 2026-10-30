//go:build unix

package processgroup

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestTerminateProcessTreeKillsBackgroundDescendantsLikeRust mirrors the pipe
// adapter's PipeChildTerminator::kill: termination reaches the whole group, not
// just the direct child, once the leader was launched with PrepareChildSession.
func TestTerminateProcessTreeKillsBackgroundDescendantsLikeRust(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "leaked")
	cmd := exec.Command("sh", "-c", `sleep 0.6; printf leaked > "$0" & wait`, marker)
	PrepareChildSession(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	pid := cmd.Process.Pid
	sid, err := unix.Getsid(pid)
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("Getsid(%d) error = %v", pid, err)
	}
	if sid != pid {
		_ = cmd.Process.Kill()
		t.Fatalf("session id = %d, want child pid %d", sid, pid)
	}
	if err := TerminateProcessTree(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("TerminateProcessTree() error = %v", err)
	}
	_ = cmd.Wait()
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("background descendant survived process-group termination")
	} else if !os.IsNotExist(err) {
		t.Fatalf("Stat(marker) error = %v", err)
	}
}

// TestTerminateProcessTreeLeavesUnrelatedGroupAloneLikeRust covers the
// non-leader fallback: a child that did not start a session must not take an
// unrelated process group down with it.
func TestTerminateProcessTreeLeavesUnrelatedGroupAloneLikeRust(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := TerminateProcessTree(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("TerminateProcessTree() error = %v", err)
	}
	if cmd.ProcessState == nil {
		_ = cmd.Wait()
	}
	if err := cmd.Process.Signal(os.Interrupt); err == nil {
		t.Fatal("non-leader child survived direct termination")
	}
}
