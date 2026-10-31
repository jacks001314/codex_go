//go:build !windows

package applypatch

import (
	"errors"
	"fmt"
	"syscall"
)

// rustDirectoryReadError is the io error Rust's std::fs::read reports for a
// directory on Unix: EISDIR.
func rustDirectoryReadError() error {
	return syscall.EISDIR
}

// rustIOMessage renders an error the way Rust's std::io::Error Display does:
// the platform message followed by " (os error N)".
func rustIOMessage(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return fmt.Sprintf("%s (os error %d)", errno.Error(), uint32(errno))
	}
	return plainIOMessage(err)
}
