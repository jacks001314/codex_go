//go:build !windows

package voicehost

import "os/exec"

func configureHiddenProcess(command *exec.Cmd) {}
