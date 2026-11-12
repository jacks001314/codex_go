//go:build !windows

package headershelper

import (
	"os/exec"
	"syscall"
)

// prepareProcess gives the helper its own process group so a timeout can
// terminate the whole tree it started.
func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
