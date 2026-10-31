//go:build windows

package mcp

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// isMCPClosedTransportError mirrors the io::ErrorKind::BrokenPipe and
// ConnectionReset checks Rust applies in should_restart_legacy. A local helper
// that exits while the client is writing reports ERROR_NO_DATA ("the pipe is
// being closed") or ERROR_BROKEN_PIPE on Windows rather than EPIPE.
func isMCPClosedTransportError(err error) bool {
	return errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, windows.ERROR_BROKEN_PIPE) ||
		errors.Is(err, windows.ERROR_NO_DATA) ||
		errors.Is(err, windows.WSAECONNRESET)
}
