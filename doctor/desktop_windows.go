//go:build windows

package doctor

import (
	"errors"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// desktopPackageFamily is the MSIX identity Rust's platform::installed_app
// queries for the ChatGPT/Codex desktop application.
const desktopPackageFamily = "OpenAI.Codex_2p2nqsd0c76g0"

const (
	desktopErrorSuccess            = 0
	desktopErrorInsufficientBuffer = 122
	desktopPackageFilterHead       = 0x10
)

var (
	desktopKernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procFindPackagesByPackageFamily = desktopKernel32.NewProc("FindPackagesByPackageFamily")
	procGetPackageFamilyName        = desktopKernel32.NewProc("GetPackageFamilyName")

	errDesktopDiscovery = errors.New("desktop application discovery failed")
)

func desktopInstalledApp() (*desktopApp, error) {
	family, err := syscall.UTF16PtrFromString(desktopPackageFamily)
	if err != nil {
		return nil, err
	}
	var count, length uint32
	result, _, _ := procFindPackagesByPackageFamily.Call(
		uintptr(unsafe.Pointer(family)),
		uintptr(desktopPackageFilterHead),
		uintptr(unsafe.Pointer(&count)),
		0,
		uintptr(unsafe.Pointer(&length)),
		0,
		0,
	)
	if uint32(result) == desktopErrorSuccess && count == 0 {
		return nil, nil
	}
	if uint32(result) != desktopErrorInsufficientBuffer {
		return nil, errDesktopDiscovery
	}
	names := make([]*uint16, count)
	buffer := make([]uint16, length)
	result, _, _ = procFindPackagesByPackageFamily.Call(
		uintptr(unsafe.Pointer(family)),
		uintptr(desktopPackageFilterHead),
		uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&names[0])),
		uintptr(unsafe.Pointer(&length)),
		uintptr(unsafe.Pointer(&buffer[0])),
		0,
	)
	if uint32(result) != desktopErrorSuccess {
		return nil, errDesktopDiscovery
	}
	if count == 0 {
		return nil, nil
	}
	version, ok := desktopPackageVersion(desktopUTF16String(buffer))
	if !ok {
		return nil, errDesktopDiscovery
	}
	return &desktopApp{identity: "OpenAI.Codex", version: version}, nil
}

// desktopPackageVersion pulls the version segment out of the package full name
// the way Rust does: OpenAI.Codex_<version>_<arch>__<family>.
func desktopPackageVersion(packageName string) (string, bool) {
	rest, ok := strings.CutPrefix(packageName, "OpenAI.Codex_")
	if !ok {
		return "", false
	}
	version, _, _ := strings.Cut(rest, "_")
	if version == "" {
		return "", false
	}
	for _, part := range strings.Split(version, ".") {
		if part == "" {
			return "", false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return "", false
			}
		}
	}
	return version, true
}

func desktopApplicationRunning(_ *desktopApp, pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var length uint32
	result, _, _ := procGetPackageFamilyName.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&length)),
		0,
	)
	if uint32(result) != desktopErrorInsufficientBuffer || length == 0 {
		return false
	}
	family := make([]uint16, length)
	result, _, _ = procGetPackageFamilyName.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&length)),
		uintptr(unsafe.Pointer(&family[0])),
	)
	if uint32(result) != desktopErrorSuccess {
		return false
	}
	return desktopUTF16String(family) == desktopPackageFamily
}

func desktopUTF16String(value []uint16) string {
	for index, unit := range value {
		if unit == 0 {
			return syscall.UTF16ToString(value[:index])
		}
	}
	return syscall.UTF16ToString(value)
}
