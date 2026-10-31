//go:build !windows

package applypatch

import (
	"errors"
	"fmt"
	"syscall"
)

// rustIOMessage renders an error the way Rust's std::io::Error Display does:
// the platform message followed by " (os error N)".
func rustIOMessage(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return fmt.Sprintf("%s (os error %d)", errno.Error(), uint32(errno))
	}
	return plainIOMessage(err)
}
