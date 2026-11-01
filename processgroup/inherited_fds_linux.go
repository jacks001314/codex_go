//go:build linux

package processgroup

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// HideInheritedDescriptors marks every descriptor above stderr close-on-exec
// for the duration of one launch and returns the undo.
//
// Rust parity: codex-rs/utils/pty/src/linux_fds.rs
// close_inherited_fds_except (#47605). A spawned child must start with only the
// descriptors its launcher hands it, so whatever the daemon happened to have
// open cannot be read or written from the sandboxed command. Go forks inside
// the runtime and offers no post-fork hook, so the marking happens in the
// parent just before Start; the flags are put back as soon as the child has
// forked, by which point its descriptor table is a private copy.
func HideInheritedDescriptors() (restore func()) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return func() {}
	}
	changed := make([]int, 0, len(entries))
	for _, entry := range entries {
		fd, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || fd <= 2 {
			continue
		}
		flags, flagErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if flagErr != nil || flags&unix.FD_CLOEXEC != 0 {
			continue
		}
		if _, setErr := unix.FcntlInt(uintptr(fd), unix.F_SETFD, flags|unix.FD_CLOEXEC); setErr == nil {
			changed = append(changed, fd)
		}
	}
	if len(changed) == 0 {
		return func() {}
	}
	return func() {
		for _, fd := range changed {
			flags, flagErr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
			if flagErr != nil {
				continue
			}
			_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, flags&^unix.FD_CLOEXEC)
		}
	}
}
