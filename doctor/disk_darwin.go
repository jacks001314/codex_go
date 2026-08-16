//go:build darwin

package doctor

import "golang.org/x/sys/unix"

// platformAvailableDiskSpace mirrors the Unix implementation but uses the
// darwin Statfs_t block size field (Bsize; Linux/BSD use Frsize).
func platformAvailableDiskSpace(path string) (uint64, bool) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, false
	}
	if stat.Files > 0 && stat.Ffree == 0 {
		return 0, true
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), true
}
