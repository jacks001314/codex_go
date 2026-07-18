//go:build windows

package windowssandbox

import (
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

func AllowNullDevice(sidString string) error {
	sidString = strings.TrimSpace(sidString)
	if sidString == "" {
		return ErrInvalidRequest
	}
	sidBytes, err := SIDBytesFromString(sidString)
	if err != nil {
		return err
	}
	sid := sidPointerFromBytes(sidBytes)
	if sid == nil || !sid.IsValid() {
		return ErrInvalidRequest
	}

	name, err := windows.UTF16PtrFromString(`\\.\NUL`)
	if err != nil {
		return err
	}
	const desired = windows.READ_CONTROL | windows.WRITE_DAC
	handle, err := windows.CreateFile(
		name,
		desired,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		runtime.KeepAlive(sidBytes)
		return nil
	}
	defer windows.CloseHandle(handle)

	sd, err := windows.GetSecurityInfo(handle, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		runtime.KeepAlive(sidBytes)
		return nil
	}
	dacl, _, err := sd.DACL()
	if err != nil && err != windows.ERROR_OBJECT_NOT_FOUND {
		runtime.KeepAlive(sidBytes)
		runtime.KeepAlive(sd)
		return nil
	}
	const mask = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE
	if daclAllowsMaskForSID(dacl, sid, mask) {
		runtime.KeepAlive(sidBytes)
		runtime.KeepAlive(sd)
		return nil
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessPermissions: mask,
		AccessMode:        windows.SET_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
	newACL, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, dacl)
	if err != nil {
		runtime.KeepAlive(sidBytes)
		runtime.KeepAlive(sd)
		return nil
	}
	err = windows.SetSecurityInfo(handle, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, newACL, nil)
	runtime.KeepAlive(sidBytes)
	runtime.KeepAlive(sd)
	runtime.KeepAlive(newACL)
	runtime.KeepAlive(entry)
	return nil
}
