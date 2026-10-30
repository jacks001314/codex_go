//go:build unix

package appserver

import (
	"golang.org/x/sys/unix"
)

// raiseManagedDaemonNoFileLimit raises the soft RLIMIT_NOFILE to 4096, capped
// by the inherited hard limit, and preserves an existing higher soft limit.
// Rust managed_daemon::raise_nofile_limit.
func raiseManagedDaemonNoFileLimit() error {
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		return err
	}
	target := managedDaemonNoFileTarget(limit.Cur, limit.Max)
	if target == limit.Cur {
		return nil
	}
	limit.Cur = target
	return unix.Setrlimit(unix.RLIMIT_NOFILE, &limit)
}
