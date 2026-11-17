//go:build windows

package tcptunnel

import (
	"os"

	"golang.org/x/sys/windows"
)

// openTokenFile opens with FILE_SHARE_DELETE so the one-use file can be removed
// while its handle stays open, matching Rust's std file open on Windows.
func openTokenFile(path string) (*os.File, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
