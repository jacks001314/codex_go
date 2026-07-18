//go:build windows

package appserver

import osexec "os/exec"

func prepareCommandExecProcess(cmd *osexec.Cmd) {}

func terminateCommandExecProcess(active *managedCommandExec) {
	if process := active.commandProcessHandle(); process != nil {
		_ = process.Kill()
	}
}
