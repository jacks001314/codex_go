//go:build windows

package windowssandbox

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// LogonExistingSandboxAccount opens a token for one existing managed account
// without provisioning or changing it (Rust
// codex_windows_sandbox::logon_existing_sandbox_account). The caller must keep
// the authenticated credential directory pinned and must still verify the
// token's recorded SID, group membership and non-administrator status.
//
// Unlike the app-side readers this service recovery path never creates
// diagnostic files beneath an owner-controlled directory.
func LogonExistingSandboxAccount(codexHome string, account SandboxRuntimeAccount) (windows.Token, error) {
	if _, err := os.Stat(SetupMarkerPath(codexHome)); err != nil {
		return 0, fmt.Errorf("read sandbox setup marker: %w", err)
	}
	marker, err := ReadSetupMarker(codexHome)
	if err != nil {
		return 0, fmt.Errorf("read sandbox setup marker: %w", err)
	}
	if marker == nil || !marker.VersionMatches() {
		return 0, errors.New("sandbox setup marker is missing or incompatible")
	}
	users, err := ReadSandboxUsersFile(codexHome)
	if err != nil {
		return 0, fmt.Errorf("read sandbox accounts: %w", err)
	}
	if users == nil || !users.VersionMatches() {
		return 0, errors.New("sandbox accounts are incompatible")
	}
	var record SandboxUserRecord
	switch account {
	case SandboxRuntimeAccountOffline:
		record = users.Offline
	case SandboxRuntimeAccountOnline:
		record = users.Online
	default:
		return 0, fmt.Errorf("unknown sandbox runtime account %q", account)
	}
	if !strings.EqualFold(record.Username, account.Username()) {
		return 0, errors.New("sandbox account record does not match the managed account")
	}
	password, err := DecodeSandboxUserPassword(record)
	if err != nil {
		return 0, err
	}
	return logonSandboxAccountToken(record.Username, ".", password)
}

// logonSandboxAccountToken logs on one managed account and returns its token.
func logonSandboxAccountToken(username string, domainValue string, password string) (windows.Token, error) {
	usernamePtr, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return 0, err
	}
	domain, err := windows.UTF16PtrFromString(domainValue)
	if err != nil {
		return 0, err
	}
	passwordPtr, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return 0, err
	}
	var token windows.Handle
	ok, _, callErr := procLogonUserW.Call(
		uintptr(unsafe.Pointer(usernamePtr)),
		uintptr(unsafe.Pointer(domain)),
		uintptr(unsafe.Pointer(passwordPtr)),
		logon32LogonInteractive,
		logon32ProviderDefault,
		uintptr(unsafe.Pointer(&token)),
	)
	if ok == 0 {
		errno, _ := callErr.(syscall.Errno)
		return 0, sandboxAccountLogonFailure(uint32(errno), errno)
	}
	if token == 0 {
		return 0, errors.New("log on existing sandbox account: no token")
	}
	return windows.Token(token), nil
}
