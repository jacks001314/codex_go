//go:build linux

package sandboxpath

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// CurrentUserCanModify mirrors Rust `current_user_can_modify` (Rust #51211):
// whether the effective user can write to - or first chmod - the path.
//
// Metadata that cannot be read means the candidate is unverified, so it is
// reported as modifiable and never trusted. An owner can chmod a currently
// read-only file or directory before writing it, so ownership alone is enough.
// Otherwise the kernel checks the effective credentials, including POSIX ACLs;
// libc's faccessat emulation ignores ACLs, so the check uses faccessat2(2) like
// Rust does.
func CurrentUserCanModify(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid == uint32(os.Geteuid()) {
		return true
	}
	return canModifyFromAccessError(unix.Faccessat2(unix.AT_FDCWD, path, unix.W_OK, unix.AT_EACCESS))
}

// canModifyFromAccessError classifies the permission check the way Rust does:
// only EACCES and EROFS prove the user cannot modify the path. Any other error
// (including ENOSYS from a pre-5.8 kernel) must not turn an unverified candidate
// into a trusted one.
func canModifyFromAccessError(err error) bool {
	if err == nil {
		return true
	}
	return !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EROFS)
}
