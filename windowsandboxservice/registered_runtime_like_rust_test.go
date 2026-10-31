//go:build windows

package windowsandboxservice

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

// runtimeRecord builds one registered-Core record for the validation tests.
func runtimeRecord(accounts ...windowssandbox.RuntimeAccountRegistration) *windowssandbox.InstallationRecord {
	alias := "C:\\Users\\codex-sandbox-offline\\AppData\\Local\\Microsoft\\WindowsApps"
	runtimeAccounts := make([]windowssandbox.RuntimeAccountRegistration, 0, len(accounts))
	for index, account := range accounts {
		account.AliasPath = &alias
		if account.UserSID == "" {
			account.UserSID = "S-1-5-21-1-2-3-100" + string(rune('0'+index))
		}
		runtimeAccounts = append(runtimeAccounts, account)
	}
	return &windowssandbox.InstallationRecord{
		UserSID:   "S-1-5-21-1-2-3-1000",
		CodexHome: `C:\Users\owner`,
		Runtime: &windowssandbox.RuntimeRegistration{
			PackageFamily: "OpenAI.Codex_2p2nqsd0c76g0",
			Accounts:      runtimeAccounts,
		},
	}
}

// useRuntimeSeams installs fake OS seams for the duration of one test.
func useRuntimeSeams(
	t *testing.T,
	resolve func(string) (string, error),
	localFlags func(string) (uint32, bool, error),
	groups func(windows.Token, uint32) ([]runtimeGroup, error),
	userSID func(windows.Token) (string, error),
	load func(windows.Token, string) (windows.Handle, error),
	unload func(windows.Token, windows.Handle) error,
) {
	t.Helper()
	previousResolve := runtimeResolveSIDString
	previousLocalFlags := runtimeLocalUserFlags
	previousGroups := runtimeTokenGroupSIDs
	previousUserSID := runtimeUserSIDString
	previousLoad := runtimeLoadUserProfile
	previousUnload := runtimeUnloadUserProfile
	t.Cleanup(func() {
		runtimeResolveSIDString = previousResolve
		runtimeLocalUserFlags = previousLocalFlags
		runtimeTokenGroupSIDs = previousGroups
		runtimeUserSIDString = previousUserSID
		runtimeLoadUserProfile = previousLoad
		runtimeUnloadUserProfile = previousUnload
	})
	if resolve != nil {
		runtimeResolveSIDString = resolve
	}
	if localFlags != nil {
		runtimeLocalUserFlags = localFlags
	}
	if groups != nil {
		runtimeTokenGroupSIDs = groups
	}
	if userSID != nil {
		runtimeUserSIDString = userSID
	}
	if load != nil {
		runtimeLoadUserProfile = load
	}
	if unload != nil {
		runtimeUnloadUserProfile = unload
	}
}

func TestValidateRuntimeRecordRejectsDuplicateAccountsLikeRust(t *testing.T) {
	record := runtimeRecord(
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOffline},
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOffline},
	)
	if err := ValidateRuntimeRecord(record); err == nil || err.Error() != "runtime registration record contains duplicate accounts" {
		t.Fatalf("ValidateRuntimeRecord(duplicates) error = %v", err)
	}
}

func TestValidateRuntimeRecordRejectsExtraAccountsLikeRust(t *testing.T) {
	record := runtimeRecord(
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOffline},
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOnline},
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOnline},
	)
	if err := ValidateRuntimeRecord(record); err == nil || err.Error() != "runtime registration record has too many accounts" {
		t.Fatalf("ValidateRuntimeRecord(three accounts) error = %v", err)
	}
}

func TestValidateRuntimeRecordRequiresRuntimeOwnershipLikeRust(t *testing.T) {
	record := &windowssandbox.InstallationRecord{}
	if err := ValidateRuntimeRecord(record); err == nil {
		t.Fatal("ValidateRuntimeRecord(no runtime) = nil")
	}
}

func TestPrepareRuntimeCleanupRefusesRetiringAndPendingLogonsLikeRust(t *testing.T) {
	retiring := "generation-a"
	record := runtimeRecord(
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOffline},
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOnline},
	)
	record.Runtime.Retiring = &retiring
	useRuntimeSeams(t, func(name string) (string, error) {
		if name == windowssandbox.OfflineUsername {
			return "S-1-5-21-1-2-3-1000", nil
		}
		return "S-1-5-21-1-2-3-1001", nil
	}, func(string) (uint32, bool, error) { return 0x10, true, nil }, nil, nil, nil, nil)
	if _, err := PrepareRuntimeCleanup(record); err == nil || err.Error() != "interrupted runtime cleanup requires repair" {
		t.Fatalf("PrepareRuntimeCleanup(retiring) error = %v", err)
	}

	record = runtimeRecord(
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOffline, UserSID: "S-1-5-21-1-2-3-1000", CleanupLogonPending: true},
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOnline, UserSID: "S-1-5-21-1-2-3-1001"},
	)
	useRuntimeSeams(t, func(name string) (string, error) {
		if name == windowssandbox.OfflineUsername {
			return "S-1-5-21-1-2-3-1000", nil
		}
		return "S-1-5-21-1-2-3-1001", nil
	}, func(string) (uint32, bool, error) { return 0x10, true, nil }, nil, nil, nil, nil)
	if _, err := PrepareRuntimeCleanup(record); err == nil || err.Error() != "cleanup logon recovery is pending" {
		t.Fatalf("PrepareRuntimeCleanup(pending logon) error = %v", err)
	}
}

func TestPrepareRuntimeCleanupRejectsReplacedLocalAccountLikeRust(t *testing.T) {
	record := runtimeRecord(
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOffline, UserSID: "S-1-5-21-9-9-9-1001"},
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOnline, UserSID: "S-1-5-21-9-9-9-1002"},
	)
	useRuntimeSeams(t, func(name string) (string, error) {
		return "S-1-5-21-9-9-9-5555", nil
	}, func(string) (uint32, bool, error) { return 0x10, true, nil }, nil, nil, nil, nil)
	if _, err := PrepareRuntimeCleanup(record); err == nil || err.Error() != "managed runtime account was replaced before cleanup" {
		t.Fatalf("PrepareRuntimeCleanup(replaced account) error = %v", err)
	}
}

func TestPrepareRuntimeCleanupSkipsMissingLocalAccountsLikeRust(t *testing.T) {
	record := runtimeRecord(
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOffline, UserSID: "S-1-5-21-9-9-9-1001"},
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOnline, UserSID: "S-1-5-21-9-9-9-1002"},
	)
	resolved := false
	useRuntimeSeams(t, func(string) (string, error) {
		resolved = true
		return "", errors.New("should not resolve a removed account")
	}, func(string) (uint32, bool, error) { return 0, false, nil }, nil, nil, nil, nil)
	prepared, err := PrepareRuntimeCleanup(record)
	if err != nil {
		t.Fatalf("PrepareRuntimeCleanup() error = %v", err)
	}
	if prepared != record || resolved {
		t.Fatalf("PrepareRuntimeCleanup() = %#v resolved=%v", prepared, resolved)
	}
}

func TestValidateRuntimeTargetRejectsChangedAndReplacedAccountsLikeRust(t *testing.T) {
	groups := func(windows.Token, uint32) ([]runtimeGroup, error) {
		return []runtimeGroup{{SID: "S-1-5-21-9-9-9-513", Attributes: windows.SE_GROUP_ENABLED}}, nil
	}
	useRuntimeSeams(t, func(string) (string, error) {
		return "S-1-5-21-9-9-9-1001", nil
	}, nil, groups, func(windows.Token) (string, error) {
		return "S-1-5-21-9-9-9-1001", nil
	}, nil, nil)
	if _, err := ValidateRuntimeTarget(0, windowssandbox.SandboxRuntimeAccountOffline, "S-1-5-21-9-9-9-1002"); err == nil || err.Error() != "managed runtime account SID changed" {
		t.Fatalf("ValidateRuntimeTarget(changed SID) error = %v", err)
	}
	useRuntimeSeams(t, func(string) (string, error) {
		return "S-1-5-21-9-9-9-4444", nil
	}, nil, groups, func(windows.Token) (string, error) {
		return "S-1-5-21-9-9-9-1001", nil
	}, nil, nil)
	if _, err := ValidateRuntimeTarget(0, windowssandbox.SandboxRuntimeAccountOffline, ""); err == nil || err.Error() != "managed runtime account was replaced" {
		t.Fatalf("ValidateRuntimeTarget(replaced account) error = %v", err)
	}
}

func TestValidateRuntimeTargetRejectsAdministratorsAndOutsidersLikeRust(t *testing.T) {
	sandboxGroup := "S-1-5-21-9-9-9-513"
	useRuntimeSeams(t, func(name string) (string, error) {
		if name == runtimeSandboxUsersGroup {
			return sandboxGroup, nil
		}
		return "S-1-5-21-9-9-9-1001", nil
	}, nil, func(windows.Token, uint32) ([]runtimeGroup, error) {
		return []runtimeGroup{{SID: "S-1-5-32-544", Attributes: windows.SE_GROUP_ENABLED}}, nil
	}, func(windows.Token) (string, error) {
		return "S-1-5-21-9-9-9-1001", nil
	}, nil, nil)
	if _, err := ValidateRuntimeTarget(0, windowssandbox.SandboxRuntimeAccountOffline, ""); err == nil || err.Error() != "managed runtime account is an administrator" {
		t.Fatalf("ValidateRuntimeTarget(administrator) error = %v", err)
	}

	useRuntimeSeams(t, func(name string) (string, error) {
		if name == runtimeSandboxUsersGroup {
			return sandboxGroup, nil
		}
		return "S-1-5-21-9-9-9-1001", nil
	}, nil, func(windows.Token, uint32) ([]runtimeGroup, error) {
		return []runtimeGroup{{SID: sandboxGroup, Attributes: 0}}, nil
	}, func(windows.Token) (string, error) {
		return "S-1-5-21-9-9-9-1001", nil
	}, nil, nil)
	if _, err := ValidateRuntimeTarget(0, windowssandbox.SandboxRuntimeAccountOffline, ""); err == nil || err.Error() != "runtime account is not in CodexSandboxUsers" {
		t.Fatalf("ValidateRuntimeTarget(disabled membership) error = %v", err)
	}
}

func TestValidateRuntimeTargetAcceptsEnabledSandboxMemberLikeRust(t *testing.T) {
	sandboxGroup := "S-1-5-21-9-9-9-513"
	useRuntimeSeams(t, func(name string) (string, error) {
		if name == runtimeSandboxUsersGroup {
			return sandboxGroup, nil
		}
		return "S-1-5-21-9-9-9-1001", nil
	}, nil, func(windows.Token, uint32) ([]runtimeGroup, error) {
		return []runtimeGroup{
			{SID: "S-1-5-32-545", Attributes: windows.SE_GROUP_ENABLED},
			{SID: sandboxGroup, Attributes: windows.SE_GROUP_ENABLED},
		}, nil
	}, func(windows.Token) (string, error) {
		return "S-1-5-21-9-9-9-1001", nil
	}, nil, nil)
	sid, err := ValidateRuntimeTarget(0, windowssandbox.SandboxRuntimeAccountOffline, "S-1-5-21-9-9-9-1001")
	if err != nil || sid != "S-1-5-21-9-9-9-1001" {
		t.Fatalf("ValidateRuntimeTarget() = %q, %v", sid, err)
	}
}

func TestRuntimeAccountProfileUnloadsBeforeReleaseLikeRust(t *testing.T) {
	unloaded := windows.Handle(0)
	useRuntimeSeams(t, func(name string) (string, error) {
		if name == runtimeSandboxUsersGroup {
			return "S-1-5-21-9-9-9-513", nil
		}
		return "S-1-5-21-9-9-9-1001", nil
	}, nil, func(windows.Token, uint32) ([]runtimeGroup, error) {
		return []runtimeGroup{{SID: "S-1-5-21-9-9-9-513", Attributes: windows.SE_GROUP_ENABLED}}, nil
	}, func(windows.Token) (string, error) {
		return "S-1-5-21-9-9-9-1001", nil
	}, func(windows.Token, string) (windows.Handle, error) {
		return 7, nil
	}, func(_ windows.Token, profile windows.Handle) error {
		unloaded = profile
		return nil
	})
	profile, err := LoadRuntimeAccountProfile(0, windowssandbox.SandboxRuntimeAccountOffline, "")
	if err != nil {
		t.Fatalf("LoadRuntimeAccountProfile() error = %v", err)
	}
	if _, err := LoadRuntimeAccountProfile(0, windowssandbox.SandboxRuntimeAccountOffline, ""); err != nil {
		t.Fatalf("LoadRuntimeAccountProfile() second error = %v", err)
	}
	if err := profile.UnloadProfile(); err != nil {
		t.Fatalf("UnloadProfile() error = %v", err)
	}
	if unloaded != 7 {
		t.Fatalf("unloaded profile = %d", unloaded)
	}
	if err := profile.UnloadProfile(); err != nil || unloaded != 7 {
		t.Fatalf("second UnloadProfile() = %v unloaded=%d", err, unloaded)
	}
	if err := profile.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRuntimeHasRetainingRegistrationChecksEveryAccountLikeRust(t *testing.T) {
	record := runtimeRecord(
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOffline, UserSID: "S-1-5-21-9-9-9-1001"},
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOnline, UserSID: "S-1-5-21-9-9-9-1002"},
	)
	queried := make([]string, 0, 2)
	previous := runtimeRegisteredPackages
	t.Cleanup(func() { runtimeRegisteredPackages = previous })
	runtimeRegisteredPackages = func(userSID string, family string) ([]string, error) {
		queried = append(queried, userSID+"|"+family)
		if userSID == "S-1-5-21-9-9-9-1002" {
			return []string{"OpenAI.Codex_2p2nqsd0c76g0_1.0.0.0_x64__abc"}, nil
		}
		return nil, nil
	}
	retaining, err := RuntimeHasRetainingRegistration(record)
	if err != nil || !retaining {
		t.Fatalf("RuntimeHasRetainingRegistration() = %v, %v", retaining, err)
	}
	if len(queried) != 2 || queried[1] != "S-1-5-21-9-9-9-1002|OpenAI.Codex_2p2nqsd0c76g0" {
		t.Fatalf("queried = %v", queried)
	}

	runtimeRegisteredPackages = func(string, string) ([]string, error) {
		return nil, nil
	}
	retaining, err = RuntimeHasRetainingRegistration(record)
	if err != nil || retaining {
		t.Fatalf("RuntimeHasRetainingRegistration(empty) = %v, %v", retaining, err)
	}
}

func TestRuntimeHasRetainingRegistrationPropagatesQueryFailuresLikeRust(t *testing.T) {
	record := runtimeRecord(
		windowssandbox.RuntimeAccountRegistration{Account: windowssandbox.SandboxRuntimeAccountOffline, UserSID: "S-1-5-21-9-9-9-1001"},
	)
	failure := errors.New("package repository unavailable")
	previous := runtimeRegisteredPackages
	t.Cleanup(func() { runtimeRegisteredPackages = previous })
	runtimeRegisteredPackages = func(string, string) ([]string, error) {
		return nil, failure
	}
	if _, err := RuntimeHasRetainingRegistration(record); !errors.Is(err, failure) {
		t.Fatalf("RuntimeHasRetainingRegistration() error = %v", err)
	}
}
