//go:build windows

package envutil

import (
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// TestSuppressConsoleWindowSetsCreateNoWindow pins the Rust #48483 contract on
// the shared helper: a freshly built child gets CREATE_NO_WINDOW.
func TestSuppressConsoleWindowSetsCreateNoWindow(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "echo")
	SuppressConsoleWindow(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil, want CREATE_NO_WINDOW")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}

// TestSuppressConsoleWindowPreservesExistingFlags guards the Job Object callers
// (`gitutil`, hook and updater trees): Go assigns creation flags directly, so
// suppressing the window must OR the flag in rather than replace the set.
func TestSuppressConsoleWindowPreservesExistingFlags(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "echo")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	SuppressConsoleWindow(cmd)
	flags := cmd.SysProcAttr.CreationFlags
	if flags&windows.CREATE_SUSPENDED == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_SUSPENDED preserved", flags)
	}
	if flags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NO_WINDOW", flags)
	}
}

// TestSuppressConsoleWindowToleratesNilCommand keeps the helper safe on the
// error paths where no command was built.
func TestSuppressConsoleWindowToleratesNilCommand(t *testing.T) {
	SuppressConsoleWindow(nil)
}
