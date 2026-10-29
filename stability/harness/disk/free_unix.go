//go:build linux || darwin

package disk

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// FreeBytes reports the space available to the current user on the volume that
// holds path.
func FreeBytes(path string) (uint64, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(absolute, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
