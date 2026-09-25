//go:build !windows

package codemode

import "os/exec"

// suppressCodeModeHostConsoleWindow is a no-op off Windows.
func suppressCodeModeHostConsoleWindow(*exec.Cmd) {}
