//go:build windows

package appserver

import (
	osexec "os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// Mirrors Rust's #48483 regression test expectation on the Go side: a piped
// `command/exec` child carries CREATE_NO_WINDOW so it never allocates a console
// window.
func TestPrepareCommandExecProcessSuppressesConsoleWindowLikeRust(t *testing.T) {
	cmd := osexec.Command("cmd", "/c", "echo")
	prepareCommandExecProcess(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#v, want CREATE_NO_WINDOW", cmd.SysProcAttr)
	}
}

// Mirrors Rust's `prepare_suspended_spawn`: the console-window suppression must
// not drop an existing CREATE_SUSPENDED, because Go's callers set creation flags
// directly where Rust's `creation_flags` replaces them.
func TestSuppressChildConsoleWindowPreservesSuspendedFlagsLikeRust(t *testing.T) {
	cmd := osexec.Command("cmd", "/c", "echo")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	suppressChildConsoleWindow(cmd)
	flags := cmd.SysProcAttr.CreationFlags
	if flags&windows.CREATE_SUSPENDED == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_SUSPENDED preserved", flags)
	}
	if flags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NO_WINDOW", flags)
	}
}
