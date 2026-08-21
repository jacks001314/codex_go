//go:build darwin

package execserver

import (
	"os"
	"syscall"
	"time"
)

// createdAtMillis reports the darwin birth time when the filesystem provides
// it, falling back to zero otherwise (mirrors the Linux statx behavior from
// Rust #39666).
func createdAtMillis(info os.FileInfo) int64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Birthtimespec.Sec != 0 {
		return time.Unix(stat.Birthtimespec.Sec, stat.Birthtimespec.Nsec).UTC().UnixNano() / int64(time.Millisecond)
	}
	return 0
}

// createdAtMillisForPath returns the no-follow birth time for a resolved path.
// darwin falls back to the info-based helper (no statx equivalent).
func createdAtMillisForPath(info os.FileInfo, _ string) int64 {
	return createdAtMillis(info)
}
