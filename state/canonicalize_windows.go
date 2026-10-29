//go:build windows

package state

import (
	"strings"

	"golang.org/x/sys/windows"
)

// canonicalizePath resolves a path the way Rust's std::fs::canonicalize does:
// mount-point reparse points (junctions) are followed too, which
// filepath.EvalSymlinks does not do. Managed rollout roots are compared in this
// canonical form so a nested junction cannot escape Codex home (Rust #42135).
func canonicalizePath(path string) (string, error) {
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
			return strings.TrimPrefix(resolved, `\\?\`), nil
		}
		size = written + 1
	}
}
