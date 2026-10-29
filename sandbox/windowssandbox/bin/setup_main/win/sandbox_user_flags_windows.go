//go:build windows

package win

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const nerrUserNotFound uint32 = 2221

var (
	procNetUserGetInfo   = modnetapi32.NewProc("NetUserGetInfo")
	procNetApiBufferFree = modnetapi32.NewProc("NetApiBufferFree")
)

// ReadLocalUserFlags reports a local account's USER_INFO_1 flags. The second
// result is false when the account does not exist, mirroring Rust's
// local_user_flags returning Ok(None).
func ReadLocalUserFlags(name string) (uint32, bool, error) {
	nameW, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, false, err
	}
	var buffer unsafe.Pointer
	status, _, _ := procNetUserGetInfo.Call(
		0,
		uintptr(unsafe.Pointer(nameW)),
		1,
		uintptr(unsafe.Pointer(&buffer)),
	)
	if uint32(status) == nerrUserNotFound {
		return 0, false, nil
	}
	if uint32(status) != nerrSuccess {
		return 0, false, fmt.Errorf("NetUserGetInfo failed for %s code %d", name, status)
	}
	defer procNetApiBufferFree.Call(uintptr(buffer))
	return (*userInfo1)(buffer).Flags, true, nil
}
