//go:build windows

package windowssandbox

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
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

const (
	privateDesktopPrefix     = "CodexSandboxDesktop-"
	desktopDeleteAccess      = 0x00010000
	desktopParticipantAccess = desktopAllAccess &^ (desktopDeleteAccess | windows.WRITE_DAC | windows.WRITE_OWNER)
)

var (
	procCreateDesktopW = windows.NewLazySystemDLL("user32.dll").NewProc("CreateDesktopW")
	procCloseDesktop   = windows.NewLazySystemDLL("user32.dll").NewProc("CloseDesktop")
	procOpenDesktopW   = windows.NewLazySystemDLL("user32.dll").NewProc("OpenDesktopW")

	sharedPrivateDesktopsMu sync.Mutex
	sharedPrivateDesktops   = map[string]*LaunchDesktop{}
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

// ValidatePrivateDesktopName accepts only names created by this package. It
// deliberately rejects the default station/desktop and injected path forms.
func ValidatePrivateDesktopName(name string) error {
	nonce, ok := strings.CutPrefix(name, privateDesktopPrefix)
	if !ok || nonce == "" || len(nonce) > 32 {
		return fmt.Errorf("invalid private desktop name")
	}
	for _, ch := range nonce {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return fmt.Errorf("invalid private desktop name")
		}
	}
	return nil
}

// OpenPrivateLaunchDesktop opens a parent-owned private desktop without
// creating one or falling back to the default desktop.
func OpenPrivateLaunchDesktop(name string) (*LaunchDesktop, error) {
	if err := ValidatePrivateDesktopName(name); err != nil {
		return nil, err
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := openDesktop(namePtr)
	if err != nil {
		return nil, fmt.Errorf("OpenDesktopW failed for %s: %w", name, err)
	}
	return newLaunchDesktopValue(name, `Winsta0\`+name, uintptr(handle)), nil
}

// SharedPrivateDesktopForUser returns a private desktop name that stays alive
// in the parent process and is shared only for the same sandbox account and
// effective permissions.
func SharedPrivateDesktopForUser(username string, policy DesktopPolicy) (string, error) {
	sandboxSID, err := resolveSandboxAccountSID(username)
	if err != nil {
		return "", err
	}
	ownerSID, err := currentUserSIDString()
	if err != nil {
		return "", err
	}
	return sharedPrivateDesktopForSIDs(ownerSID, sandboxSID, policy)
}

// PrepareLegacyLaunchDesktop opens the shared desktop matching the restricted
// legacy token's account and effective permissions.
func PrepareLegacyLaunchDesktop(security *LegacySessionSecurity, permissions *ResolvedWindowsSandboxPermissions, commandCWD string, envMap map[string]string, additionalDenyWritePaths []string, logsBaseDir string) (*LaunchDesktop, error) {
	if security == nil || security.Token == 0 {
		return nil, ErrInvalidRequest
	}
	sandboxSIDBytes, err := getUserSIDBytes(windows.Token(security.Token))
	if err != nil {
		return nil, err
	}
	sandboxSID := StringFromSIDBytes(sandboxSIDBytes)
	if sandboxSID == "" {
		return nil, fmt.Errorf("sandbox token has no user SID")
	}
	policy := NewLegacyDesktopPolicy(permissions, commandCWD, envMap, security, additionalDenyWritePaths)
	name, err := sharedPrivateDesktopForSIDs(sandboxSID, sandboxSID, policy)
	if err != nil {
		return nil, err
	}
	return OpenPrivateLaunchDesktop(name)
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

func createDesktopWithSDDL(name string, sddl string) (*LaunchDesktop, error) {
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("parse private desktop security descriptor: %w", err)
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	r1, _, e1 := procCreateDesktopW.Call(
		uintptr(unsafe.Pointer(namePtr)),
		0,
		0,
		0,
		uintptr(desktopAllAccess),
		uintptr(unsafe.Pointer(&attributes)),
	)
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(attributes)
	runtime.KeepAlive(namePtr)
	if r1 == 0 {
		if e1 != syscall.Errno(0) {
			return nil, fmt.Errorf("CreateDesktopW failed for %s: %w", name, e1)
		}
		return nil, fmt.Errorf("CreateDesktopW failed for %s: %w", name, windows.GetLastError())
	}
	return newLaunchDesktopValue(name, `Winsta0\`+name, r1), nil
}

func openDesktop(name *uint16) (windows.Handle, error) {
	r1, _, e1 := procOpenDesktopW.Call(
		uintptr(unsafe.Pointer(name)),
		0,
		0,
		uintptr(desktopParticipantAccess),
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

func sharedPrivateDesktopForSIDs(ownerSID string, sandboxSID string, policy DesktopPolicy) (string, error) {
	key := ownerSID + "\x00" + sandboxSID + "\x00" + policy.Key()
	sharedPrivateDesktopsMu.Lock()
	defer sharedPrivateDesktopsMu.Unlock()
	if desktop := sharedPrivateDesktops[key]; desktop != nil {
		return desktop.Name, nil
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	name := privateDesktopPrefix + strings.ReplaceAll(id.String(), "-", "")
	sddl := fmt.Sprintf("D:P(A;;0x%x;;;%s)(A;;0x%x;;;%s)", desktopAllAccess, ownerSID, desktopParticipantAccess, sandboxSID)
	desktop, err := createDesktopWithSDDL(name, sddl)
	if err != nil {
		return "", err
	}
	sharedPrivateDesktops[key] = desktop
	return name, nil
}

func currentUserSIDString() (string, error) {
	token, err := GetCurrentTokenForRestriction()
	if err != nil {
		return "", err
	}
	defer CloseTokenHandle(token)
	sidBytes, err := getUserSIDBytes(windows.Token(token))
	if err != nil {
		return "", err
	}
	sid := StringFromSIDBytes(sidBytes)
	if sid == "" {
		return "", fmt.Errorf("current token has no user SID")
	}
	return sid, nil
}
