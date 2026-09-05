//go:build windows

package voicehost

import (
	"os/exec"
	"syscall"
)

func configureHiddenProcess(command *exec.Cmd) {
	if command == nil {
		return
	}
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
