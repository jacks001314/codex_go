//go:build !windows

package appserver

import (
	osexec "os/exec"
	"syscall"
)

func prepareCommandExecProcess(cmd *osexec.Cmd) {
	if cmd != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

func terminateCommandExecProcess(active *managedCommandExec) {
	process := active.commandProcessHandle()
	if process == nil {
		return
	}
	_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
}
