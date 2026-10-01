//go:build windows

package appserver

import (
	osexec "os/exec"
	"testing"

	"codex_go/envutil"
	"golang.org/x/sys/windows"
)

// TestStartHookProcessTreeKeepsConsoleSuppressionLikeRust pins the Job Object
// hook path: startHookProcessTree assigns SysProcAttr outright to suspend the
// child for containment, which used to drop the CREATE_NO_WINDOW the launcher
// had already applied and made every hook flash a terminal window whenever the
// app server owned no console (Rust #48483). The containment flag and the
// suppression must both survive.
func TestStartHookProcessTreeKeepsConsoleSuppressionLikeRust(t *testing.T) {
	cmd := osexec.Command("cmd", "/c", "exit 0")
	envutil.SuppressConsoleWindow(cmd)
	tree, err := startHookProcessTree(cmd)
	if err != nil {
		t.Fatalf("startHookProcessTree failed: %v", err)
	}
	defer tree.terminate()

	flags := cmd.SysProcAttr.CreationFlags
	if flags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NO_WINDOW preserved", flags)
	}
	if flags&windows.CREATE_SUSPENDED == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_SUSPENDED", flags)
	}
	if err := tree.wait(); err != nil {
		t.Fatalf("hook tree wait failed: %v", err)
	}
}
