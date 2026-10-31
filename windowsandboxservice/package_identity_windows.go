//go:build windows

package windowsandboxservice

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

const (
	// The OEM metadata pair marks a preinstalled package (Rust
	// PackageLifecycle::new reads it from the installed location).
	oemPackageMetadataDir  = "microsoft.system.package.metadata"
	oemPackageMetadataFile = "Custom.data"
)

var getPackagePathByFullName = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetPackagePathByFullName")

// lifecyclePreinstalled is the seam the lifecycle captures the OEM marker
// through.
var lifecyclePreinstalled = packagePreinstalled

// packagePreinstalled reports whether the running package carries OEM install
// metadata. Windows removes that metadata on uninstall, so the lifecycle
// captures it while the package is still installed.
func packagePreinstalled() bool {
	fullName, present, err := windowssandbox.CurrentProcessPackageFullName()
	if err != nil || !present {
		return false
	}
	location, err := packageInstalledLocation(fullName)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(location, oemPackageMetadataDir, oemPackageMetadataFile))
	return err == nil
}

// packageInstalledLocation reads GetPackagePathByFullName for one full name.
func packageInstalledLocation(fullName string) (string, error) {
	name, err := windows.UTF16PtrFromString(fullName)
	if err != nil {
		return "", err
	}
	var length uint32
	status, _, _ := getPackagePathByFullName.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&length)),
		0,
	)
	if uint32(status) != uint32(windows.ERROR_INSUFFICIENT_BUFFER) || length == 0 {
		return "", fmt.Errorf("read package installation path: error %d", uint32(status))
	}
	buffer := make([]uint16, length)
	status, _, _ = getPackagePathByFullName.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(&length)),
		uintptr(unsafe.Pointer(&buffer[0])),
	)
	if uint32(status) != 0 {
		return "", fmt.Errorf("read package installation path: error %d", uint32(status))
	}
	return windows.UTF16ToString(buffer), nil
}
