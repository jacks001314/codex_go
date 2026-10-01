//go:build windows

package appserverdaemon

import (
	"strings"

	"golang.org/x/sys/windows"
)

// resolveFinalPath resolves a path the way Rust's canonicalize does: a Windows
// mount-point reparse point (the daemon's installer junction) is followed too,
// which filepath.EvalSymlinks does not do.
func resolveFinalPath(path string) (string, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	size := uint32(len(path) + 64)
	for {
		buffer := make([]uint16, size)
		written, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], size, 0)
		if err != nil {
			return "", err
		}
		if written < size {
			resolved := windows.UTF16ToString(buffer[:written])
			// The handle-based name carries the verbatim prefix, while the rest
			// of the package code compares ordinary absolute paths.
			return strings.TrimPrefix(resolved, `\\?\`), nil
		}
		size = written + 1
	}
}
