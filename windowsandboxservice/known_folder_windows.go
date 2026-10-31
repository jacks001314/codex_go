//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Known Folder identifiers the registered runtime needs to reach the owner's
// metadata roots (Rust registered_runtime/known_folder.rs).
var (
	knownFolderLocalAppData = windows.GUID{
		Data1: 0xF1B32785, Data2: 0x6FBA, Data3: 0x4FCF,
		Data4: [8]byte{0x9D, 0x55, 0x7B, 0x8E, 0x7F, 0x15, 0x70, 0x91},
	}
	knownFolderRoamingAppData = windows.GUID{
		Data1: 0x3EB685DB, Data2: 0x65F9, Data3: 0x4CF6,
		Data4: [8]byte{0xA0, 0x3A, 0xE3, 0xEF, 0x65, 0x72, 0x9F, 0x3D},
	}
)

const (
	// kfFlagDontVerify and kfFlagNoPackageRedirection keep owner metadata
	// roots exactly as the owner sees them, outside package redirection.
	kfFlagDontVerify           = 0x00004000
	kfFlagNoPackageRedirection = 0x00008000
	// ownerFolderFlags is what Rust uses for the owner's two AppData roots.
	ownerFolderFlags = kfFlagDontVerify | kfFlagNoPackageRedirection
	// maxKnownFolderUnits bounds the returned UTF-16 path.
	maxKnownFolderUnits = 32768
)

var (
	modRuntimeShell32               = windows.NewLazySystemDLL("shell32.dll")
	modRuntimeOle32                 = windows.NewLazySystemDLL("ole32.dll")
	procRuntimeSHGetKnownFolderPath = modRuntimeShell32.NewProc("SHGetKnownFolderPath")
	procRuntimeCoTaskMemFree        = modRuntimeOle32.NewProc("CoTaskMemFree")
)

// knownFolderPath resolves one Known Folder for an explicit token and flags
// (Rust known_folder::path). The returned allocation is always freed.
func knownFolderPath(token windows.Token, folder *windows.GUID, flags uint32) (string, error) {
	if folder == nil {
		return "", errors.New("missing Known Folder identifier")
	}
	var raw *uint16
	status, _, _ := procRuntimeSHGetKnownFolderPath.Call(
		uintptr(unsafe.Pointer(folder)),
		uintptr(flags),
		uintptr(token),
		uintptr(unsafe.Pointer(&raw)),
	)
	if raw != nil {
		defer procRuntimeCoTaskMemFree.Call(uintptr(unsafe.Pointer(raw)))
	}
	if hresultFailed(status) {
		return "", fmt.Errorf("resolve Known Folder: %w", hresultError(uint32(status)))
	}
	if raw == nil {
		return "", errors.New("Known Folder path missing")
	}
	units := unsafe.Slice(raw, maxKnownFolderUnits)
	length := 0
	for length < maxKnownFolderUnits && units[length] != 0 {
		length++
	}
	if length >= maxKnownFolderUnits {
		return "", errors.New("Known Folder path exceeds bound")
	}
	return windows.UTF16ToString(units[:length]), nil
}
