//go:build !windows

package appserver

import (
	osexec "os/exec"
	"syscall"

	"codex_go/processgroup"
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
	_ = processgroup.Kill(process.Pid)
}
