//go:build windows

package gitutil

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// TestStartGitTreeSuppressesConsoleWindowLikeRust pins the git tree spawn: the
// Job Object containment used to assign SysProcAttr outright, so `git` ran
// without CREATE_NO_WINDOW and every invocation popped a terminal window when
// the app server owned no console (Rust #48483). Containment and suppression
// must both be present.
func TestStartGitTreeSuppressesConsoleWindowLikeRust(t *testing.T) {
	respawn := func() *exec.Cmd { return exec.Command("cmd", "/c", "exit 0") }
	cmd := respawn()
	tree, err := startGitTree(cmd, respawn)
	if err != nil {
		t.Fatalf("startGitTree failed: %v", err)
	}
	defer tree.kill()

	flags := cmd.SysProcAttr.CreationFlags
	if flags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NO_WINDOW", flags)
	}
	if flags&windows.CREATE_SUSPENDED == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_SUSPENDED", flags)
	}
	if err := tree.wait(); err != nil {
		t.Fatalf("git tree wait failed: %v", err)
	}
}
