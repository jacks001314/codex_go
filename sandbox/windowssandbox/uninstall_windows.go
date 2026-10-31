//go:build windows

package windowssandbox

import (
	"errors"
	"fmt"
	"path/filepath"
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
	modUserenv           = windows.NewLazySystemDLL("userenv.dll")
	procNetUserDel       = modNetapi32Del.NewProc("NetUserDel")
	procNetLocalGroupDel = modNetapi32Del.NewProc("NetLocalGroupDel")
	procDeleteProfileW   = modUserenv.NewProc("DeleteProfileW")
)

// cleanUpLegacyWindowsSandbox mirrors Rust uninstall_windows::
// clean_up_legacy_windows_sandbox (#50437).
//
// Cleanup serializes with setup through the machine-wide
// `Global\CodexSandboxSetup` mutex, disables the sandbox accounts, waits for
// their processes and logon tokens to exit, and then removes each independent
// resource. The registered-runtime installation record remains service-owned
// and has no Go carrier yet.
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
	lock, err := AcquireSandboxSetupLock(5000)
	if err != nil {
		return err
	}
	defer lock.Release()
	if _, err := prepareSandboxCleanupForRemoval(); err != nil {
		return err
	}
	// The legacy path has no pinned Codex home, so it keeps every sandbox
	// directory and its filesystem permissions (Rust
	// clean_up_legacy_windows_sandbox -> clean_up_packaged_windows_sandbox(None)).
	return finishSandboxCleanup(nil, report, nil, defaultSandboxCleanupSteps())
}

// cleanUpPackagedWindowsSandbox removes resources created for one authenticated
// installation. The home and its ancestors stay pinned until cleanUpDesktop
// runs, while the sandbox accounts remain disabled (Rust #42375).
func cleanUpPackagedWindowsSandbox(codexHome string, report func(string), cleanUpDesktop func() error) error {
	if report == nil {
		report = func(string) {}
	}
	if err := validateCleanupCodexHome(codexHome); err != nil {
		return err
	}
	elevated, err := isElevated()
	if err != nil {
		return fmt.Errorf("determine elevation state: %w", err)
	}
	if !elevated {
		return errors.New("sandbox cleanup requires administrator privileges")
	}
	lock, err := AcquireSandboxSetupLock(5000)
	if err != nil {
		return err
	}
	defer lock.Release()
	if _, err := prepareSandboxCleanupForRemoval(); err != nil {
		return err
	}
	return finishSandboxCleanup(&codexHome, report, cleanUpDesktop, defaultSandboxCleanupSteps())
}

// prepareSandboxCleanupForRemoval disables the sandbox accounts and waits for
// their processes and logon tokens to exit before any protection is removed.
// Nothing has been torn down yet, so a preparation failure restores the
// original account flags and keeps the setup lock released.
func prepareSandboxCleanupForRemoval() (*disabledSandboxUsers, error) {
	users := &disabledSandboxUsers{}
	if err := users.disable(); err != nil {
		var failures []string
		failures = append(failures, err.Error())
		if restoreErr := users.restore(); restoreErr != nil {
			failures = append(failures, restoreErr.Error())
		}
		return nil, errors.New(strings.Join(failures, "; "))
	}
	if err := stopSandboxCleanupProcesses(users); err != nil {
		var failures []string
		failures = append(failures, err.Error())
		if restoreErr := users.restore(); restoreErr != nil {
			failures = append(failures, restoreErr.Error())
		}
		return nil, errors.New(strings.Join(failures, "; "))
	}
	return users, nil
}

type sandboxCleanupSteps struct {
	removeWFPFilters    func() error
	removeFirewallRules func() error
	unhideUsers         func() error
	removeUsers         func(func(string)) error
	removeGroup         func() error
}

func defaultSandboxCleanupSteps() sandboxCleanupSteps {
	return sandboxCleanupSteps{
		removeWFPFilters:    removeLegacyWFPFilters,
		removeFirewallRules: removeLegacyFirewallRules,
		unhideUsers: func() error {
			return unhideSandboxUsers([]string{OfflineUsername, OnlineUsername})
		},
		removeUsers: removeLegacySandboxUsers,
		removeGroup: removeLegacySandboxGroup,
	}
}

func finishSandboxCleanup(codexHome *string, report func(string), cleanUpDesktop func() error, steps sandboxCleanupSteps) error {
	if report == nil {
		report = func(string) {}
	}
	var failures []string
	if codexHome != nil {
		if err := validateCleanupCodexHome(*codexHome); err != nil {
			return err
		}
		failures = append(failures, removeSandboxDirectories(*codexHome, report)...)
	} else {
		report("skipping sandbox directories: no pinned home")
	}
	if cleanUpDesktop != nil {
		if err := cleanUpDesktop(); err != nil {
			message := fmt.Sprintf("clean up desktop: failed, %v", err)
			report(message)
			failures = append(failures, message)
		}
	}
	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"remove WFP filters", steps.removeWFPFilters},
		{"remove firewall rules", steps.removeFirewallRules},
		{"remove hidden-user entries", steps.unhideUsers},
	} {
		if step.run == nil {
			continue
		}
		if err := step.run(); err != nil {
			message := fmt.Sprintf("%s: failed, %v", step.name, err)
			report(message)
			failures = append(failures, message)
			continue
		}
		report(step.name + ": completed")
	}
	if steps.removeUsers != nil {
		if err := steps.removeUsers(report); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if steps.removeGroup != nil {
		if err := removeLegacySandboxGroupAfter(failures, report, steps.removeGroup); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func validateCleanupCodexHome(codexHome string) error {
	if strings.TrimSpace(codexHome) == "" {
		return errors.New("sandbox cleanup requires a pinned Codex home")
	}
	if !filepath.IsAbs(codexHome) {
		return errors.New("sandbox cleanup requires an absolute Codex home")
	}
	return nil
}

// removeLegacySandboxGroupAfter keeps the machine-wide sandbox group while any
// earlier cleanup step failed, so a retry can still resolve the sandbox
// accounts (Rust uninstall_windows::finish removes the group only when cleanup
// produced no errors).
func removeLegacySandboxGroupAfter(failures []string, report func(string), remove func() error) error {
	if report == nil {
		report = func(string) {}
	}
	if len(failures) > 0 {
		report("retaining sandbox group until cleanup finishes")
		return nil
	}
	if err := remove(); err != nil {
		message := fmt.Sprintf("remove sandbox group: failed, %v", err)
		report(message)
		return errors.New(message)
	}
	report("remove sandbox group: completed")
	return nil
}

// removeLegacySandboxUsers deletes each sandbox profile before its account.
// A profile deletion failure preserves the account for a later retry (Rust
// principals::DisabledSandboxUsers::remove_users).
func removeLegacySandboxUsers(report func(string)) error {
	if report == nil {
		report = func(string) {}
	}
	var failures []string
	fail := func(message string) {
		report(message)
		failures = append(failures, message)
	}
	for _, name := range []string{OfflineUsername, OnlineUsername} {
		sid, exists, err := lookupLocalUserSID(name)
		if err != nil {
			fail(fmt.Sprintf("look up sandbox account %s: %v", name, err))
			continue
		}
		if !exists {
			report(fmt.Sprintf("skipping %s: account not found", name))
			continue
		}
		if err := deleteUserProfile(sid); err != nil {
			fail(fmt.Sprintf("remove %s profile and account: failed, %v", name, err))
			continue
		}
		namePtr, err := windows.UTF16PtrFromString(name)
		if err != nil {
			fail(fmt.Sprintf("remove %s profile and account: failed, %v", name, err))
			continue
		}
		status, _, _ := procNetUserDel.Call(0, uintptr(unsafe.Pointer(namePtr)))
		if code := uint32(status); code != 0 && code != nerrUserNotFound {
			fail(fmt.Sprintf("remove %s profile and account: failed, code %d", name, code))
			continue
		}
		report(fmt.Sprintf("removed %s profile and account", name))
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func lookupLocalUserSID(name string) (string, bool, error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", false, err
	}
	sidBuffer := make([]byte, 64)
	sidSize := uint32(len(sidBuffer))
	domainBuffer := make([]uint16, 256)
	domainSize := uint32(len(domainBuffer))
	var use uint32
	err = windows.LookupAccountName(nil, namePtr, (*windows.SID)(unsafe.Pointer(&sidBuffer[0])),
		&sidSize, &domainBuffer[0], &domainSize, &use)
	if err != nil {
		if errors.Is(err, windows.ERROR_NONE_MAPPED) || errors.Is(err, windows.ERROR_ACCOUNT_RESTRICTION) {
			return "", false, nil
		}
		return "", false, err
	}
	sid := (*windows.SID)(unsafe.Pointer(&sidBuffer[0]))
	return sid.String(), true, nil
}

func deleteUserProfile(sid string) error {
	sidPtr, err := windows.UTF16PtrFromString(sid)
	if err != nil {
		return err
	}
	status, _, _ := procDeleteProfileW.Call(uintptr(unsafe.Pointer(sidPtr)), 0, 0)
	code := uint32(status)
	if code == 0 || code == 2 || code == 1515 {
		return nil
	}
	return fmt.Errorf("DeleteProfileW code %d", code)
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
