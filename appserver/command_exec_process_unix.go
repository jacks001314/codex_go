//go:build !windows

package appserver

import (
	osexec "os/exec"

	"codex_go/processgroup"
)

func prepareCommandExecProcess(cmd *osexec.Cmd) {
	// Rust #47605: piped command children use ChildCommand's NewSession mode,
	// which detaches the controlling terminal (setsid) and adds the Linux
	// parent-death signal. A new session also makes the child its group leader,
	// so process-group cleanup keeps working.
	processgroup.PrepareChildSession(cmd)
}

func terminateCommandExecProcess(active *managedCommandExec) {
	process := active.commandProcessHandle()
	if process == nil {
		return
	}
	_ = processgroup.TerminateProcessTree(process)
}
