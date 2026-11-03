//go:build windows

package execserver

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openRegularFileForBoundWrite creates a missing file or opens an existing one
// without truncating it. FILE_FLAG_OPEN_REPARSE_POINT keeps a reparse point at
// the final component from redirecting the bound handle (Rust #58974).
func openRegularFileForBoundWrite(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_ALWAYS,
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

// rejectMultipleLinks refuses a file another path can still mutate.
func rejectMultipleLinks(file *os.File) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks > 1 {
		return errors.New("output file has multiple links")
	}
	return nil
}
