//go:build !windows

package exec

import (
	"errors"
	"fmt"
	"syscall"
)

// rustOSErrorMessage renders an OS error the way Rust's io::Error Display does:
// the platform message followed by " (os error N)".
func rustOSErrorMessage(err error) (string, bool) {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return "", false
	}
	return fmt.Sprintf("%s (os error %d)", errno.Error(), uint32(errno)), true
}
