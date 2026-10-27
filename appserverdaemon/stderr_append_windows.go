//go:build windows

package appserverdaemon

import (
	"os"

	"golang.org/x/sys/windows"
)

var procReOpenFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

// enableStderrAppendMode replaces the process's stderr handle with an
// append-only reopen of the same file, so writes after a truncation follow EOF
// instead of leaving a sparse hole (Rust #55856 `enable_append`).
func enableStderrAppendMode() error {
	handle, _, callErr := procReOpenFile.Call(
		uintptr(windows.Stderr),
		uintptr(windows.FILE_GENERIC_WRITE&^windows.FILE_WRITE_DATA),
		uintptr(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE),
		0,
	)
	if handle == 0 || handle == uintptr(windows.InvalidHandle) {
		return callErr
	}
	reopened := windows.Handle(handle)
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, reopened); err != nil {
		_ = windows.CloseHandle(reopened)
		return err
	}
	// Keep Go's cached stderr on the append-only handle as well.
	os.Stderr = os.NewFile(uintptr(reopened), "/dev/stderr")
	return nil
}
