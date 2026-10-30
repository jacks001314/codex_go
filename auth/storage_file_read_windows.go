//go:build windows

package auth

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// readAuthFile reads a credential file while sharing deletion, so a concurrent
// atomic replacement never fails because this reader holds the destination
// (Rust std opens credential files with FILE_SHARE_DELETE).
func readAuthFile(path string) ([]byte, error) {
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, &os.PathError{Op: "read", Path: path, Err: err}
	}
	return data, nil
}
