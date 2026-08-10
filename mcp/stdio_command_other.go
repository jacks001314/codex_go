//go:build !windows

package mcp

import (
	"os/exec"

	"codex_go/envutil"
)

func newMCPStdioCommand(command string, args ...string) *exec.Cmd {
	cmd := exec.Command(command, args...)
	envutil.ScrubCommandEnv(cmd)
	return cmd
}
