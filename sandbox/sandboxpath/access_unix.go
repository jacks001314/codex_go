//go:build !linux && !windows

package sandboxpath

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// CurrentUserCanModify mirrors the Linux rule for the other Unix hosts. The
// pre-sandbox bubblewrap filter is Linux-only, so this build is a portability
// shim that keeps the same conservative semantics.
func CurrentUserCanModify(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid == uint32(os.Geteuid()) {
		return true
	}
	return canModifyFromAccessError(unix.Access(path, unix.W_OK))
}

func canModifyFromAccessError(err error) bool {
	if err == nil {
		return true
	}
	return !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EROFS)
}
