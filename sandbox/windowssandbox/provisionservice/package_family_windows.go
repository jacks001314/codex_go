//go:build windows

package provisionservice

import (
	"errors"
	"fmt"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetPackageFamilyName = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetPackageFamilyName")

const (
	winErrorInsufficientBuffer uint32 = 122
	winAppModelNoPackage       uint32 = 15700
	maxPackageFamilyUnits      uint32 = 256
)

// currentPackageFamily ports Rust process_package_family: absence is not
// authorization and the OS-assigned family is validated before routing.
func currentPackageFamily() (string, bool, error) {
	process := windows.CurrentProcess()
	var length uint32
	status, _, _ := procGetPackageFamilyName.Call(uintptr(process), uintptr(unsafe.Pointer(&length)), 0)
	switch uint32(status) {
	case winAppModelNoPackage:
		return "", false, nil
	case winErrorInsufficientBuffer:
	default:
		return "", false, fmt.Errorf("query the process package family: windows error %d", status)
	}
	if length == 0 || length > maxPackageFamilyUnits {
		return "", false, errors.New("the process package family has an invalid length")
	}
	buffer := make([]uint16, length)
	status, _, _ = procGetPackageFamilyName.Call(
		uintptr(process),
		uintptr(unsafe.Pointer(&length)),
		uintptr(unsafe.Pointer(&buffer[0])),
	)
	if uint32(status) != 0 {
		return "", false, fmt.Errorf("read the process package family: windows error %d", status)
	}
	if int(length) > len(buffer) {
		return "", false, errors.New("the package-family API returned an invalid length")
	}
	units := buffer[:length]
	if len(units) == 0 || units[len(units)-1] != 0 {
		return "", false, errors.New("the process package family is not null-terminated")
	}
	units = units[:len(units)-1]
	if len(units) == 0 || !validPackageFamilyUnits(units) {
		return "", false, errors.New("the process package family is malformed")
	}
	return string(utf16.Decode(units)), true, nil
}

func validPackageFamilyUnits(units []uint16) bool {
	for index := 0; index < len(units); index++ {
		unit := units[index]
		if unit == 0 {
			return false
		}
		if !utf16.IsSurrogate(rune(unit)) {
			continue
		}
		if unit >= 0xDC00 || index+1 >= len(units) {
			return false
		}
		next := units[index+1]
		if next < 0xDC00 || next > 0xDFFF {
			return false
		}
		index++
	}
	return true
}
