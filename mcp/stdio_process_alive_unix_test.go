//go:build unix

package mcp

import (
	"errors"
	"syscall"
)

// mcpTestProcessAlive reports whether a process still exists: signal 0 succeeds
// for a live process and returns EPERM when the process exists but is owned by
// another user.
func mcpTestProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
