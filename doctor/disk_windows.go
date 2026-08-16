//go:build windows

package doctor

import "golang.org/x/sys/windows"

func platformAvailableDiskSpace(path string) (uint64, bool) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(pointer, &free, nil, nil); err != nil {
		return 0, false
	}
	return free, true
}
