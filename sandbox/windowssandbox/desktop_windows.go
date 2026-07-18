//go:build windows

package windowssandbox

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

const desktopAllAccess = 0x0001 |
	0x0002 |
	0x0004 |
	0x0008 |
	0x0010 |
	0x0020 |
	0x0040 |
	0x0080 |
	0x00010000 |
	windows.READ_CONTROL |
	windows.WRITE_DAC |
	windows.WRITE_OWNER

var (
	procCreateDesktopW = windows.NewLazySystemDLL("user32.dll").NewProc("CreateDesktopW")
	procCloseDesktop   = windows.NewLazySystemDLL("user32.dll").NewProc("CloseDesktop")
)

func PrepareLaunchDesktop(usePrivateDesktop bool) (*LaunchDesktop, error) {
	if !usePrivateDesktop {
		return newLaunchDesktopValue("Default", `Winsta0\Default`, 0), nil
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}
	name := "CodexSandboxDesktop-" + strings.ReplaceAll(id.String(), "-", "")
	return CreateLaunchDesktop(name)
}

func CreateLaunchDesktop(name string) (*LaunchDesktop, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrInvalidRequest
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := createDesktop(namePtr)
	if err != nil {
		return nil, fmt.Errorf("CreateDesktopW failed for %s: %w", name, err)
	}
	desktop := &LaunchDesktop{
		Name:        name,
		StartupName: `Winsta0\` + name,
		Handle:      uintptr(handle),
	}
	desktop.startupWide = ToWide(desktop.StartupName)
	if err := grantDesktopAccess(handle); err != nil {
		_ = desktop.Close()
		return nil, err
	}
	return desktop, nil
}

func (d *LaunchDesktop) StartupInfoDesktop() *uint16 {
	if d == nil || len(d.startupWide) == 0 {
		return nil
	}
	return &d.startupWide[0]
}

func (d *LaunchDesktop) Close() error {
	if d == nil || d.Handle == 0 {
		return nil
	}
	err := closeDesktop(windows.Handle(d.Handle))
	d.Handle = 0
	return err
}

func createDesktop(name *uint16) (windows.Handle, error) {
	r1, _, e1 := procCreateDesktopW.Call(
		uintptr(unsafe.Pointer(name)),
		0,
		0,
		0,
		uintptr(desktopAllAccess),
		0,
	)
	if r1 == 0 {
		if e1 != syscall.Errno(0) {
			return 0, e1
		}
		return 0, windows.GetLastError()
	}
	return windows.Handle(r1), nil
}

func closeDesktop(handle windows.Handle) error {
	r1, _, e1 := procCloseDesktop.Call(uintptr(handle))
	if r1 == 0 {
		if e1 != syscall.Errno(0) {
			return e1
		}
		return windows.GetLastError()
	}
	return nil
}

func grantDesktopAccess(handle windows.Handle) error {
	tokenHandle, err := GetCurrentTokenForRestriction()
	if err != nil {
		return err
	}
	token := windows.Token(tokenHandle)
	defer token.Close()
	logonSID, err := getLogonSIDBytes(token)
	if err != nil {
		return err
	}
	sid := sidPointerFromBytes(logonSID)
	entry := windows.EXPLICIT_ACCESS{
		AccessPermissions: desktopAllAccess,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, nil)
	if err != nil {
		runtime.KeepAlive(logonSID)
		return err
	}
	err = windows.SetSecurityInfo(handle, windows.SE_WINDOW_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	runtime.KeepAlive(logonSID)
	runtime.KeepAlive(acl)
	runtime.KeepAlive(entry)
	return err
}

func newLaunchDesktopValue(name string, startupName string, handle uintptr) *LaunchDesktop {
	return &LaunchDesktop{
		Name:        name,
		StartupName: startupName,
		Handle:      handle,
		startupWide: ToWide(startupName),
	}
}
