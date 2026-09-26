//go:build windows

package appserver

import osexec "os/exec"

// prepareCommandExecProcess mirrors Rust's piped spawn default for a
// `command/exec` child: the pty wrapper sets CREATE_NO_WINDOW so the child never
// allocates a console window (Rust #48483).
func prepareCommandExecProcess(cmd *osexec.Cmd) {
	suppressChildConsoleWindow(cmd)
}

func terminateCommandExecProcess(active *managedCommandExec) {
	if process := active.commandProcessHandle(); process != nil {
		_ = process.Kill()
	}
}
