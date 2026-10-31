//go:build !windows

package mcp

import (
	"errors"
	"syscall"
)

// isMCPClosedTransportError mirrors the io::ErrorKind::BrokenPipe and
// ConnectionReset checks Rust applies in should_restart_legacy.
func isMCPClosedTransportError(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}
