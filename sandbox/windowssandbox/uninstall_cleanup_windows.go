//go:build windows

package windowssandbox

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ufAccountDisable uint32 = 0x0002

	nerrSuccess uint32 = 0

	wtsCurrentServerHandle = 0
)

var (
	modNetapi32Cleanup   = windows.NewLazySystemDLL("netapi32.dll")
	procNetUserGetInfo   = modNetapi32Cleanup.NewProc("NetUserGetInfo")
	procNetUserSetInfo   = modNetapi32Cleanup.NewProc("NetUserSetInfo")
	procNetApiBufferFree = modNetapi32Cleanup.NewProc("NetApiBufferFree")

	modWtsapi32          = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSEnumerateProc = modWtsapi32.NewProc("WTSEnumerateProcessesW")

	modSecur32             = windows.NewLazySystemDLL("secur32.dll")
	procLsaEnumerateLogons = modSecur32.NewProc("LsaEnumerateLogonSessions")
	procLsaGetLogonData    = modSecur32.NewProc("LsaGetLogonSessionData")
	procLsaFreeBuffer      = modSecur32.NewProc("LsaFreeReturnBuffer")

	procLsaNtStatusToWinError = windows.NewLazySystemDLL("advapi32.dll").NewProc("LsaNtStatusToWinError")

	// The function variables keep the destructive orchestration testable
	// without requiring a host with sandbox accounts and live sandbox tokens.
	readSandboxCleanupUserFlags  = readCleanupLocalUserFlags
	writeSandboxCleanupUserFlags = writeCleanupLocalUserFlags
	resolveSandboxCleanupUserSID = lookupLocalUserSID
	stopSandboxCleanupProcesses  = stopSandboxProcesses
)

type cleanupUserInfo1 struct {
	Name        *uint16
	Password    *uint16
	PasswordAge uint32
	Priv        uint32
	HomeDir     *uint16
	Comment     *uint16
	Flags       uint32
	ScriptPath  *uint16
}

type cleanupUserInfo1008 struct {
	Flags uint32
}

type disabledSandboxUser struct {
	name          string
	originalFlags uint32
	sid           string
}

type disabledSandboxUsers struct {
	users []disabledSandboxUser
}

func (u *disabledSandboxUsers) disable() error {
	for _, name := range []string{OfflineUsername, OnlineUsername} {
		flags, found, err := readSandboxCleanupUserFlags(name)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		sid, found, err := resolveSandboxCleanupUserSID(name)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("sandbox account %s disappeared during uninstall", name)
		}
		u.users = append(u.users, disabledSandboxUser{name: name, originalFlags: flags, sid: sid})
		if err := writeSandboxCleanupUserFlags(name, flags|ufAccountDisable); err != nil {
			return err
		}
	}
	return nil
}

func (u *disabledSandboxUsers) sids() []string {
	sids := make([]string, 0, len(u.users))
	for _, user := range u.users {
		sids = append(sids, user.sid)
	}
	return sids
}

func (u *disabledSandboxUsers) restore() error {
	var failures []string
	for _, user := range u.users {
		if err := writeSandboxCleanupUserFlags(user.name, user.originalFlags); err != nil {
			var errno windows.Errno
			if errors.As(err, &errno) && uint32(errno) == nerrUserNotFound {
				continue
			}
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func readCleanupLocalUserFlags(name string) (uint32, bool, error) {
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
	switch uint32(status) {
	case nerrSuccess:
	case nerrUserNotFound:
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("read local sandbox user %s: %w", name, windows.Errno(status))
	}
	defer procNetApiBufferFree.Call(uintptr(buffer))
	return (*cleanupUserInfo1)(buffer).Flags, true, nil
}

func writeCleanupLocalUserFlags(name string, flags uint32) error {
	nameW, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	info := cleanupUserInfo1008{Flags: flags}
	status, _, _ := procNetUserSetInfo.Call(
		0,
		uintptr(unsafe.Pointer(nameW)),
		1008,
		uintptr(unsafe.Pointer(&info)),
		0,
	)
	if uint32(status) != nerrSuccess {
		return fmt.Errorf("set local sandbox user %s flags: %w", name, windows.Errno(status))
	}
	return nil
}

type wtsProcessInfo struct {
	SessionID   uint32
	ProcessID   uint32
	ProcessName *uint16
	UserSID     *windows.SID
}

func stopSandboxProcesses(users *disabledSandboxUsers) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		handles, err := sandboxProcessHandles(users)
		if err != nil {
			return err
		}
		for _, handle := range handles {
			if err := windows.TerminateProcess(handle, 1); err != nil {
				if event, _ := windows.WaitForSingleObject(handle, 0); event != windows.WAIT_OBJECT_0 {
					for _, h := range handles {
						_ = windows.CloseHandle(h)
					}
					return fmt.Errorf("stop sandbox process before uninstall: %w", err)
				}
			}
		}
		for _, handle := range handles {
			remaining := time.Until(deadline)
			if remaining < 0 {
				remaining = 0
			}
			event, err := windows.WaitForSingleObject(handle, uint32(remaining/time.Millisecond))
			if err != nil {
				for _, h := range handles {
					_ = windows.CloseHandle(h)
				}
				return fmt.Errorf("wait for sandbox process exit: %w", err)
			}
			if event == uint32(windows.WAIT_TIMEOUT) {
				for _, h := range handles {
					_ = windows.CloseHandle(h)
				}
				return errors.New("sandbox process did not exit before the uninstall deadline")
			}
			if event != windows.WAIT_OBJECT_0 {
				for _, h := range handles {
					_ = windows.CloseHandle(h)
				}
				return fmt.Errorf("unexpected sandbox process wait result %d", event)
			}
		}
		for _, handle := range handles {
			_ = windows.CloseHandle(handle)
		}
		remaining, err := sandboxLogonSessionRemains(users)
		if err != nil {
			return err
		}
		if !remaining {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("sandbox processes or logon tokens remain after the uninstall deadline")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func sandboxProcessHandles(users *disabledSandboxUsers) ([]windows.Handle, error) {
	if len(users.sids()) == 0 {
		return nil, nil
	}
	var processInfo *wtsProcessInfo
	var count uint32
	ok, _, callErr := procWTSEnumerateProc.Call(
		wtsCurrentServerHandle,
		0,
		1,
		uintptr(unsafe.Pointer(&processInfo)),
		uintptr(unsafe.Pointer(&count)),
	)
	if ok == 0 {
		return nil, fmt.Errorf("enumerate sandbox processes for uninstall: %w", callErr)
	}
	if processInfo == nil || count == 0 {
		return nil, nil
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(processInfo)))
	processes := unsafe.Slice(processInfo, count)
	handles := make([]windows.Handle, 0)
	for i := range processes {
		process := &processes[i]
		if !isSandboxUserSID(users, process.UserSID) {
			continue
		}
		handle, err := windows.OpenProcess(
			windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE,
			false,
			process.ProcessID,
		)
		if err != nil {
			if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
				continue
			}
			for _, h := range handles {
				_ = windows.CloseHandle(h)
			}
			return nil, fmt.Errorf("open sandbox process for uninstall: %w", err)
		}
		var token windows.Token
		if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
			event, _ := windows.WaitForSingleObject(handle, 0)
			if event == windows.WAIT_OBJECT_0 {
				_ = windows.CloseHandle(handle)
				continue
			}
			_ = windows.CloseHandle(handle)
			for _, h := range handles {
				_ = windows.CloseHandle(h)
			}
			return nil, fmt.Errorf("identify sandbox process before uninstall: %w", err)
		}
		tokenUser, tokenErr := token.GetTokenUser()
		_ = token.Close()
		if tokenErr != nil {
			_ = windows.CloseHandle(handle)
			for _, h := range handles {
				_ = windows.CloseHandle(h)
			}
			return nil, fmt.Errorf("read sandbox process token: %w", tokenErr)
		}
		if tokenUser != nil && tokenUser.User.Sid != nil && isSandboxSID(users, tokenUser.User.Sid.String()) {
			handles = append(handles, handle)
			continue
		}
		_ = windows.CloseHandle(handle)
	}
	return handles, nil
}

type lsaLastInterLogonInfo struct {
	LastSuccessfulLogon                        int64
	LastFailedLogon                            int64
	FailedAttemptCountSinceLastSuccessfulLogon uint32
}

type securityLogonSessionData struct {
	Size                  uint32
	LogonID               windows.LUID
	UserName              windows.NTUnicodeString
	LogonDomain           windows.NTUnicodeString
	AuthenticationPackage windows.NTUnicodeString
	LogonType             uint32
	Session               uint32
	SID                   *windows.SID
	LogonTime             int64
	LogonServer           windows.NTUnicodeString
	DnsDomainName         windows.NTUnicodeString
	Upn                   windows.NTUnicodeString
	UserFlags             uint32
	LastLogonInfo         lsaLastInterLogonInfo
	LogonScript           windows.NTUnicodeString
	ProfilePath           windows.NTUnicodeString
	HomeDirectory         windows.NTUnicodeString
	HomeDirectoryDrive    windows.NTUnicodeString
	LogoffTime            int64
	KickOffTime           int64
	PasswordLastSet       int64
	PasswordCanChange     int64
	PasswordMustChange    int64
}

func sandboxLogonSessionRemains(users *disabledSandboxUsers) (bool, error) {
	var count uint32
	var logons *windows.LUID
	status, _, _ := procLsaEnumerateLogons.Call(
		uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&logons)),
	)
	if uint32(status) != 0 {
		return false, fmt.Errorf("enumerate sandbox logon sessions for uninstall: 0x%x", uint32(status))
	}
	if logons == nil || count == 0 {
		return false, nil
	}
	defer procLsaFreeBuffer.Call(uintptr(unsafe.Pointer(logons)))
	sessions := unsafe.Slice(logons, count)
	for i := range sessions {
		logon := &sessions[i]
		if logon.LowPart == 0x3e7 && logon.HighPart == 0 {
			continue
		}
		var data *securityLogonSessionData
		status, _, _ := procLsaGetLogonData.Call(
			uintptr(unsafe.Pointer(logon)),
			uintptr(unsafe.Pointer(&data)),
		)
		if uint32(status) != 0 {
			code, _, _ := procLsaNtStatusToWinError.Call(uintptr(status))
			if uint32(code) == uint32(windows.ERROR_NO_SUCH_LOGON_SESSION) {
				continue
			}
			return false, fmt.Errorf("read sandbox logon session for uninstall: 0x%x", uint32(status))
		}
		if data == nil {
			return true, nil
		}
		if isSandboxUserSID(users, data.SID) {
			procLsaFreeBuffer.Call(uintptr(unsafe.Pointer(data)))
			return true, nil
		}
		procLsaFreeBuffer.Call(uintptr(unsafe.Pointer(data)))
	}
	return false, nil
}

func isSandboxUserSID(users *disabledSandboxUsers, sid *windows.SID) bool {
	if sid == nil {
		return false
	}
	return isSandboxSID(users, sid.String())
}

func isSandboxSID(users *disabledSandboxUsers, sid string) bool {
	for _, candidate := range users.sids() {
		if strings.EqualFold(candidate, sid) {
			return true
		}
	}
	return false
}

// removeSandboxDirectories removes only the three sandbox-owned directories
// beneath an explicitly supplied Codex home. Legacy cleanup passes no home and
// therefore preserves every user file.
func removeSandboxDirectories(codexHome string, report func(string)) []string {
	var failures []string
	for _, entry := range []struct {
		name string
		path string
	}{
		{".sandbox", SandboxDir(codexHome)},
		{".sandbox-secrets", SandboxSecretsDir(codexHome)},
		{".sandbox-bin", SandboxBinDir(codexHome)},
	} {
		if _, err := os.Stat(entry.path); errors.Is(err, os.ErrNotExist) {
			report("skipping " + entry.name + ": not found")
			continue
		}
		if err := os.RemoveAll(entry.path); err != nil {
			message := fmt.Sprintf("remove %s: failed, %v", entry.name, err)
			report(message)
			failures = append(failures, message)
			continue
		}
		report("removed " + entry.name)
	}
	return failures
}
