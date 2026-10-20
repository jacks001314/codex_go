//go:build unix

package sandboxpath

import (
	"os"
	"syscall"
)

// currentUserCanReplaceChild mirrors Rust's sticky-directory check: a sticky
// parent only protects a child owned by another user.
func currentUserCanReplaceChild(parent string, child string) bool {
	euid := os.Geteuid()
	if euid == 0 {
		return true
	}
	info, err := os.Stat(parent)
	if err != nil {
		return true
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Mode&syscall.S_ISVTX == 0 || int(stat.Uid) == euid {
		return true
	}
	childInfo, err := os.Lstat(child)
	if err != nil {
		return true
	}
	childStat, ok := childInfo.Sys().(*syscall.Stat_t)
	return !ok || int(childStat.Uid) == euid
}
