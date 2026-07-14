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
	if active == nil || active.cmd == nil || active.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-active.cmd.Process.Pid, syscall.SIGKILL)
}
