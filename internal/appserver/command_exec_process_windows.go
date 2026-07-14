//go:build windows

package appserver

import osexec "os/exec"

func prepareCommandExecProcess(cmd *osexec.Cmd) {}

func terminateCommandExecProcess(active *managedCommandExec) {
	if active != nil && active.cmd != nil && active.cmd.Process != nil {
		_ = active.cmd.Process.Kill()
	}
}
