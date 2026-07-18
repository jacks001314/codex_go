//go:build !unix

package app

import "os/exec"

func exitCodeFromExitError(err *exec.ExitError) int {
	if err == nil {
		return 1
	}
	return err.ExitCode()
}
