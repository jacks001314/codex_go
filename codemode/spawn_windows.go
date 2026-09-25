//go:build windows

package codemode

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// suppressCodeModeHostConsoleWindow mirrors Rust #48138: the code-mode host is a
// detached helper, so launching it must not create a console window.
func suppressCodeModeHostConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
