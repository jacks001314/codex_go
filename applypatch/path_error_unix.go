//go:build !windows

package applypatch

import (
	"errors"
	"syscall"
)

// isNotDirectory reports ENOTDIR from a component walk. A regular file where
// the patch expects a directory means the patched path does not exist yet, not
// that the walk failed: Windows reports the same shape as a missing path.
func isNotDirectory(err error) bool {
	return errors.Is(err, syscall.ENOTDIR)
}
