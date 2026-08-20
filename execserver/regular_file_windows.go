//go:build windows

package execserver

import (
	"os"

	"golang.org/x/sys/windows"
)

func openRegularFileForRead(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		// Rust #39200: FILE_FLAG_OPEN_REPARSE_POINT avoids following a reparse
		// point at the final path component for sensitive reads.
		windows.FILE_ATTRIBUTE_NORMAL|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, os.ErrInvalid
	}
	isDiskFile, typeErr := windows.GetFileType(windows.Handle(file.Fd()))
	if typeErr != nil {
		return closeFileOnError(file, typeErr)
	}
	return closeFileOnError(file, validateRegularFile(path, file, isDiskFile == windows.FILE_TYPE_DISK))
}
