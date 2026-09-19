//go:build windows

package windowssandbox

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getPackageFullName = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetPackageFullName")

// PackageFullNameForProcess ports Rust's `process_package_name`: it queries the
// OS package full name for the process, distinguishing absence from failure.
func PackageFullNameForProcess(process windows.Handle) (string, bool, error) {
	return QueryPackageName(func(length *uint32, buffer *uint16) uint32 {
		status, _, _ := getPackageFullName.Call(
			uintptr(process),
			uintptr(unsafe.Pointer(length)),
			uintptr(unsafe.Pointer(buffer)),
		)
		return uint32(status)
	}, packageNameMaxLength)
}

// CurrentProcessHasPackageIdentity ports Rust's
// `current_process_has_package_identity` (#46575): the caller's OS-assigned
// package identity decides whether sandboxed descendants keep desktop app
// context, and a launch that requested registered-core execution without an
// identity fails instead of silently degrading.
func CurrentProcessHasPackageIdentity() (bool, error) {
	_, hasIdentity, err := PackageFullNameForProcess(windows.CurrentProcess())
	if err != nil {
		return false, err
	}
	if !hasIdentity && RegisteredCoreRequested() {
		return false, errors.New("registered Core process has no package identity")
	}
	return hasIdentity, nil
}
