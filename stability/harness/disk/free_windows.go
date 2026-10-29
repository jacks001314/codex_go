//go:build windows

package disk

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// FreeBytes reports the space available to the current user on the volume that
// holds path.
func FreeBytes(path string) (uint64, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	pointer, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(pointer, &available, &total, &free); err != nil {
		return 0, err
	}
	return available, nil
}
