//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

// runtimeLoadInstallation and runtimeSaveInstallation are the seams the
// registered lifecycle reads and writes ownership through.
var (
	runtimeLoadInstallation = windowssandbox.LoadInstallation
	runtimeSaveInstallation = windowssandbox.SaveInstallation
)

var procCoCreateGuid = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateGuid")

// runtimeSetLocalUserFlags disables or re-enables one local account
// (Rust set_local_user_flags).
var runtimeSetLocalUserFlags = setRuntimeLocalUserFlags

// runtimeRecordFamily reports the recorded package family.
func runtimeRecordFamily(record *windowssandbox.InstallationRecord) string {
	if record == nil {
		return ""
	}
	runtimeRegistration, err := record.RuntimeRegistration()
	if err != nil {
		return ""
	}
	return runtimeRegistration.PackageFamily
}

// validateRuntimeAccountSID refuses a recorded account Windows replaced.
func validateRuntimeAccountSID(account windowssandbox.RuntimeAccountRegistration) error {
	resolved, err := runtimeResolveSIDString(account.Account.Username())
	if err != nil {
		return err
	}
	if resolved != account.UserSID {
		return errors.New("managed runtime account was replaced before cleanup")
	}
	return nil
}

// logonRegisteredRuntimeAccountForRecord logs on one recorded account through
// the record's home, under the retained owner token.
func logonRegisteredRuntimeAccountForRecord(
	record *windowssandbox.InstallationRecord,
	ownerToken windows.Token,
	account windowssandbox.SandboxRuntimeAccount,
) (windows.Token, error) {
	var token windows.Token
	err := withOwnerImpersonation(ownerToken, func() error {
		var pins []windows.Handle
		if err := pinExistingAncestors(windowssandbox.SandboxSecretsDir(record.CodexHome), &pins); err != nil {
			return err
		}
		defer closeHandles(pins)
		loggedOn, err := runtimeLogonExistingAccount(record.CodexHome, account)
		token = loggedOn
		return err
	})
	if err != nil {
		return 0, err
	}
	return token, nil
}

// uniqueRuntimeGeneration produces the retirement generation Rust stores as a
// GUID debug string.
func uniqueRuntimeGeneration() string {
	var guid windows.GUID
	created, _, _ := procCoCreateGuid.Call(uintptr(unsafe.Pointer(&guid)))
	if int32(uint32(created)) >= 0 {
		return formatGUID(guid)
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// windowsPowerShellPath resolves the inbox PowerShell the cleanup child runs,
// refusing a missing, non-regular or reparse-point image.
func windowsPowerShellPath() (string, error) {
	buffer := make([]uint16, 32768)
	length, _, _ := procGetSystemDirectory.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if length == 0 || int(length) >= len(buffer) {
		return "", errors.New("Windows system directory is unavailable")
	}
	system := windows.UTF16ToString(buffer[:length])
	executable := filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe")
	info, err := os.Lstat(executable)
	if err != nil {
		return "", fmt.Errorf("inspect Windows PowerShell: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid Windows PowerShell executable")
	}
	if len(executable)+len(removalScript)*2+64 >= removalCommandLineLimit {
		return "", errors.New("cleanup command exceeds the Windows command-line limit")
	}
	return executable, nil
}

// duplicateRemovalTokens copies the validated logon tokens into the exact child
// process with inheritance disabled, and records the remote handles in the plan.
// Process handle inheritance is never relaxed.
func duplicateRemovalTokens(process *os.Process, tokens []windows.Token, targets []removalTarget) error {
	if len(tokens) == 0 {
		return nil
	}
	child, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, uint32(process.Pid))
	if err != nil {
		return fmt.Errorf("duplicate managed cleanup token: %w", err)
	}
	defer windows.CloseHandle(child)
	current := windows.CurrentProcess()
	for index, token := range tokens {
		var remote windows.Handle
		if err := windows.DuplicateHandle(
			current,
			windows.Handle(token),
			child,
			&remote,
			0,
			false,
			windows.DUPLICATE_SAME_ACCESS,
		); err != nil {
			return fmt.Errorf("duplicate managed cleanup token: %w", err)
		}
		targets[index].Handle = uintptr(remote)
	}
	return nil
}

// awaitRemovalReadiness waits for the helper's READY line, bounded by the
// preparation deadline and the accepted response length.
func awaitRemovalReadiness(stdout *os.File) error {
	if stdout == nil {
		return errors.New("cleanup exited before readiness: no output pipe")
	}
	deadline := time.Now().Add(removalReadinessDeadline)
	var ready []byte
	for {
		if !time.Now().Before(deadline) {
			return errors.New("timed out preparing Windows package cleanup")
		}
		available, err := peekAvailable(windows.Handle(stdout.Fd()))
		if err != nil {
			return fmt.Errorf("cleanup exited before readiness: %w", err)
		}
		if available == 0 {
			time.Sleep(removalReadinessPoll)
			continue
		}
		var byteRead [1]byte
		if _, err := stdout.Read(byteRead[:]); err != nil {
			return fmt.Errorf("read cleanup readiness: %w", err)
		}
		ready = append(ready, byteRead[0])
		if len(ready) > removalReadinessLimit {
			return errors.New("invalid cleanup readiness response")
		}
		if byteRead[0] == '\n' {
			break
		}
	}
	if string(ready) != "READY\r\n" && string(ready) != "READY\n" {
		return errors.New("cleanup profiles were not prepared")
	}
	return nil
}

// setRuntimeLocalUserFlags writes the account flags through NetUserSetInfo
// level 1008 (Rust set_local_user_flags).
func setRuntimeLocalUserFlags(name string, flags uint32) error {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	info := struct {
		Flags uint32
	}{Flags: flags}
	status, _, _ := procRuntimeNetUserSetInfo.Call(
		0,
		uintptr(unsafe.Pointer(namePtr)),
		1008,
		uintptr(unsafe.Pointer(&info)),
		0,
	)
	if uint32(status) != netapiSuccess {
		return fmt.Errorf("set local sandbox user %s flags: %w", name, syscall.Errno(uint32(status)))
	}
	runtime.KeepAlive(namePtr)
	return nil
}
