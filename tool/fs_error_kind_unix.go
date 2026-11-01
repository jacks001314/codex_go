//go:build !windows

package tool

import (
	"errors"
	"syscall"
)

// isSymlinkLoop reports the ELOOP an O_NOFOLLOW open raises for a symlinked
// copy source. Rust's regular-file check (exec-server/src/regular_file.rs)
// reports the same rejection as InvalidInput.
func isSymlinkLoop(err error) bool {
	return errors.Is(err, syscall.ELOOP)
}

// isNotDirectoryError reports ENOTDIR from a path whose ancestor is a regular
// file. That entry does not exist, which is the shape Windows reports for the
// same path.
func isNotDirectoryError(err error) bool {
	return errors.Is(err, syscall.ENOTDIR)
}
