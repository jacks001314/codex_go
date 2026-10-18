//go:build windows

package windowssandbox

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	logon32LogonInteractive = 2
	logon32ProviderDefault  = 0
)

var (
	modAdvapi32    = windows.NewLazySystemDLL("advapi32.dll")
	procLogonUserW = modAdvapi32.NewProc("LogonUserW")
)

func logonSandboxAccount(credentials SandboxCredentials) error {
	if credentials.Username == "" || credentials.Password == "" {
		return fmt.Errorf("sandbox account credentials are incomplete")
	}
	username, err := windows.UTF16PtrFromString(credentials.Username)
	if err != nil {
		return err
	}
	domainValue := credentials.Domain
	if domainValue == "" {
		domainValue = "."
	}
	domain, err := windows.UTF16PtrFromString(domainValue)
	if err != nil {
		return err
	}
	password, err := windows.UTF16PtrFromString(credentials.Password)
	if err != nil {
		return err
	}
	var token windows.Handle
	ok, _, callErr := procLogonUserW.Call(
		uintptr(unsafe.Pointer(username)),
		uintptr(unsafe.Pointer(domain)),
		uintptr(unsafe.Pointer(password)),
		logon32LogonInteractive,
		logon32ProviderDefault,
		uintptr(unsafe.Pointer(&token)),
	)
	if ok == 0 {
		errno, _ := callErr.(syscall.Errno)
		return sandboxAccountLogonFailure(uint32(errno), errno)
	}
	_ = windows.CloseHandle(token)
	return nil
}

func resolveSandboxAccountSID(username string) (string, error) {
	accountName, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return "", err
	}
	var sidSize, domainSize, use uint32
	err = windows.LookupAccountName(nil, accountName, nil, &sidSize, nil, &domainSize, &use)
	if err != nil && err != windows.ERROR_INSUFFICIENT_BUFFER {
		return "", err
	}
	if sidSize == 0 {
		return "", fmt.Errorf("sandbox account %s has no SID", username)
	}
	sidBuffer := make([]byte, sidSize)
	domainBuffer := make([]uint16, domainSize+1)
	sid := (*windows.SID)(unsafe.Pointer(&sidBuffer[0]))
	if err := windows.LookupAccountName(nil, accountName, sid, &sidSize, &domainBuffer[0], &domainSize, &use); err != nil {
		return "", err
	}
	return sid.String(), nil
}
