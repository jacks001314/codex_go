//go:build unix

package app

import (
	"os/exec"
	"syscall"
)

func exitCodeFromExitError(err *exec.ExitError) int {
	if err == nil {
		return 1
	}
	if status, ok := err.Sys().(syscall.WaitStatus); ok {
		if status.Signaled() {
			return 128 + int(status.Signal())
		}
		if status.Exited() {
			return status.ExitStatus()
		}
	}
	return err.ExitCode()
}
