//go:build windows

package codemode

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// Mirrors Rust #48138: spawning the code-mode host must not create a console
// window, and an already-set creation flag is preserved.
func TestSuppressCodeModeHostConsoleWindowSetsCreateNoWindow(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	suppressCodeModeHostConsoleWindow(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#v", cmd.SysProcAttr)
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("existing creation flags were dropped")
	}

	fresh := exec.Command("cmd.exe")
	suppressCodeModeHostConsoleWindow(fresh)
	if fresh.SysProcAttr == nil || fresh.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("fresh creation flags = %#v", fresh.SysProcAttr)
	}

	suppressCodeModeHostConsoleWindow(nil)
}
