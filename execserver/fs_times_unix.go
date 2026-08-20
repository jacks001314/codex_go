//go:build linux

package execserver

import (
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// createdAtMillis mirrors Rust #39666: on Linux, no-follow metadata uses
// statx so created_at_ms includes the birth time when the filesystem provides
// it, falling back to zero when statx is unavailable or blocked.
func createdAtMillis(info os.FileInfo) int64 {
	_ = info
	return 0
}

func statxBirthMillis(path string) int64 {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	var statxBuf unix.Statx_t
	// AT_SYMLINK_NOFOLLOW keeps the lookup from following the final symlink.
	if err := statxCall(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW|unix.AT_STATX_SYNC_AS_STAT, unix.STATX_BTIME, &statxBuf); err != nil {
		return 0
	}
	if statxBuf.Mask&unix.STATX_BTIME == 0 {
		return 0
	}
	seconds := statxBuf.Btime.Sec
	nsec := int64(statxBuf.Btime.Nsec)
	if seconds == 0 && nsec == 0 {
		return 0
	}
	return time.Unix(seconds, nsec).UTC().UnixNano() / int64(time.Millisecond)
}

// createdAtMillisForPath returns the no-follow birth time for a resolved
// path. The Linux implementation uses statx (Rust #39666); other platforms
// fall back to the info-based helper.
func createdAtMillisForPath(info os.FileInfo, path string) int64 {
	if linuxStatxAvailable() {
		if value := statxBirthMillis(path); value != 0 {
			return value
		}
	}
	return createdAtMillis(info)
}

var linuxStatxSupport = func() bool {
	var statxBuf unix.Statx_t
	return statxCall(unix.AT_FDCWD, "/", unix.AT_SYMLINK_NOFOLLOW|unix.AT_STATX_SYNC_AS_STAT, unix.STATX_BTIME, &statxBuf) == nil
}()

func linuxStatxAvailable() bool {
	return linuxStatxSupport
}

func statxCall(dirfd int, path string, flags int, mask uint32, statxBuf *unix.Statx_t) error {
	pathBytes, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	_, _, errno := unix.Syscall6(unix.SYS_STATX, uintptr(dirfd), uintptr(unsafe.Pointer(pathBytes)), uintptr(flags), uintptr(mask), uintptr(unsafe.Pointer(statxBuf)), 0)
	if errno != 0 {
		return errno
	}
	return nil
}
