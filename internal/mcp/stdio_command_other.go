//go:build !windows

package mcp

import "os/exec"

func newMCPStdioCommand(command string, args ...string) *exec.Cmd {
	return exec.Command(command, args...)
}
