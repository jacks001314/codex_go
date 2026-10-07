//go:build windows

package windowssandbox

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// sandboxUsersGroupName is the machine-wide group the setup helper creates
// (Rust setup_provisioning::SandboxUsersGroup).
const sandboxUsersGroupName = "CodexSandboxUsers"

// NetUserDel / NetLocalGroupDel status codes that report an already-absent
// principal (Rust uninstall_windows/principals.rs).
const (
	nerrUserNotFound  uint32 = 2221
	nerrGroupNotFound uint32 = 2223
)

var (
	modNetapi32Del       = windows.NewLazySystemDLL("netapi32.dll")
	procNetUserDel       = modNetapi32Del.NewProc("NetUserDel")
	procNetLocalGroupDel = modNetapi32Del.NewProc("NetLocalGroupDel")
)

// cleanUpLegacyWindowsSandbox mirrors Rust uninstall_windows::
// clean_up_legacy_windows_sandbox (#50437).
//
// Rust also serializes against sandbox setup through the machine-wide
// `Global\CodexSandboxSetup` mutex, retires the registered runtime
// installation, and disables/waits out the sandbox accounts before removing
// them. Those primitives have no Go carrier yet (see layout.go), so the cleanup
// starts at the resource removal and reports each step it could not finish.
func cleanUpLegacyWindowsSandbox(report func(string)) error {
	if report == nil {
		report = func(string) {}
	}
	elevated, err := isElevated()
	if err != nil {
		return fmt.Errorf("determine elevation state: %w", err)
	}
	if !elevated {
		return errors.New("sandbox cleanup requires administrator privileges")
	}
	var failures []string
	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"remove WFP filters", removeLegacyWFPFilters},
		{"remove firewall rules", removeLegacyFirewallRules},
		{"remove hidden-user entries", func() error {
			return unhideSandboxUsers([]string{OfflineUsername, OnlineUsername})
		}},
		{"remove sandbox users", removeLegacySandboxUsers},
		{"remove sandbox group", removeLegacySandboxGroup},
	} {
		if err := step.run(); err != nil {
			message := fmt.Sprintf("%s: failed, %v", step.name, err)
			report(message)
			failures = append(failures, message)
			continue
		}
		report(step.name + ": completed")
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// removeLegacySandboxUsers deletes the machine-wide sandbox accounts
// (Rust principals::DisabledSandboxUsers::remove_users -> NetUserDel).
func removeLegacySandboxUsers() error {
	var failures []string
	for _, name := range []string{OfflineUsername, OnlineUsername} {
		namePtr, err := windows.UTF16PtrFromString(name)
		if err != nil {
			failures = append(failures, fmt.Sprintf("remove sandbox account %s: %v", name, err))
			continue
		}
		status, _, _ := procNetUserDel.Call(0, uintptr(unsafe.Pointer(namePtr)))
		if code := uint32(status); code != 0 && code != nerrUserNotFound {
			failures = append(failures, fmt.Sprintf("remove sandbox account %s: code %d", name, code))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// removeLegacySandboxGroup deletes the machine-wide sandbox group
// (Rust principals::remove_sandbox_principal -> NetLocalGroupDel).
func removeLegacySandboxGroup() error {
	namePtr, err := windows.UTF16PtrFromString(sandboxUsersGroupName)
	if err != nil {
		return fmt.Errorf("remove sandbox group %s: %w", sandboxUsersGroupName, err)
	}
	status, _, _ := procNetLocalGroupDel.Call(0, uintptr(unsafe.Pointer(namePtr)))
	if code := uint32(status); code != 0 && code != nerrGroupNotFound {
		return fmt.Errorf("remove sandbox group %s: code %d", sandboxUsersGroupName, code)
	}
	return nil
}

// unhideSandboxUsers deletes the Winlogon SpecialAccounts UserList values the
// setup helper wrote, tolerating a missing key or value
// (Rust hide_users::unhide_sandbox_users).
func unhideSandboxUsers(usernames []string) error {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, UserListRegistryPath, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) || errors.Is(err, syscall.ERROR_PATH_NOT_FOUND) {
			return nil
		}
		return fmt.Errorf("open sandbox hidden-user registry key: %w", err)
	}
	defer key.Close()
	var failures []string
	for _, username := range usernames {
		if username == "" {
			continue
		}
		if err := key.DeleteValue(username); err != nil && !errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			failures = append(failures, fmt.Sprintf("remove hidden sandbox user %s: %v", username, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}
