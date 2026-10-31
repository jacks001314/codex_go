//go:build windows

package windowsandboxservice

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

func useRemovalSeams(
	t *testing.T,
	record *windowssandbox.InstallationRecord,
	loadErr error,
	family string,
	flags uint32,
	found bool,
	resolve func(string) (string, error),
) {
	t.Helper()
	previousLoad := runtimeLoadInstallation
	previousSave := runtimeSaveInstallation
	previousFamily := runtimeProcessPackageFamily
	previousFlags := runtimeLocalUserFlags
	previousSet := runtimeSetLocalUserFlags
	previousResolve := runtimeResolveSIDString
	previousPackages := runtimeRegisteredPackages
	t.Cleanup(func() {
		runtimeLoadInstallation = previousLoad
		runtimeSaveInstallation = previousSave
		runtimeProcessPackageFamily = previousFamily
		runtimeLocalUserFlags = previousFlags
		runtimeSetLocalUserFlags = previousSet
		runtimeResolveSIDString = previousResolve
		runtimeRegisteredPackages = previousPackages
	})
	runtimeLoadInstallation = func() (*windowssandbox.InstallationRecord, error) {
		return record, loadErr
	}
	runtimeSaveInstallation = func(*windowssandbox.InstallationRecord) error { return nil }
	runtimeProcessPackageFamily = func() (string, bool, error) { return family, true, nil }
	runtimeLocalUserFlags = func(string) (uint32, bool, error) { return flags, found, nil }
	runtimeSetLocalUserFlags = func(string, uint32) error { return nil }
	runtimeResolveSIDString = resolve
	runtimeRegisteredPackages = func(string, string) ([]string, error) { return nil, errors.New("no package queries expected") }
}

func TestRestoreDisabledRuntimeAccountsReDisablesPendingAccountsLikeRust(t *testing.T) {
	alias := "C:\\managed\\alias.exe"
	record := &windowssandbox.InstallationRecord{
		UserSID:   "S-1-5-21-1-2-3-1000",
		CodexHome: `C:\Users\owner`,
		Runtime: &windowssandbox.RuntimeRegistration{
			PackageFamily: "OpenAI.Codex_2p2nqsd0c76g0",
			Accounts: []windowssandbox.RuntimeAccountRegistration{{
				Account:             windowssandbox.SandboxRuntimeAccountOffline,
				UserSID:             "S-1-5-21-1-2-3-1001",
				AliasPath:           &alias,
				CleanupLogonPending: true,
			}},
		},
	}
	var written []uint32
	useRemovalSeams(t, record, nil, "OpenAI.Codex_2p2nqsd0c76g0", 0, true, func(name string) (string, error) {
		return "S-1-5-21-1-2-3-1001", nil
	})
	runtimeSetLocalUserFlags = func(_ string, flags uint32) error {
		written = append(written, flags)
		return nil
	}
	if err := RestoreDisabledRuntimeAccounts(); err != nil {
		t.Fatalf("RestoreDisabledRuntimeAccounts() error = %v", err)
	}
	if len(written) != 1 || written[0] != userFlagAccountDisable {
		t.Fatalf("restored flags = %v", written)
	}
	if record.Runtime.Accounts[0].CleanupLogonPending {
		t.Fatal("cleanup logon obligation was not cleared")
	}
}

func TestRestoreDisabledRuntimeAccountsSkipsOtherPackagesLikeRust(t *testing.T) {
	record := &windowssandbox.InstallationRecord{
		Runtime: &windowssandbox.RuntimeRegistration{PackageFamily: "OpenAI.Codex_other"},
	}
	useRemovalSeams(t, record, nil, "OpenAI.Codex_2p2nqsd0c76g0", 0, true, nil)
	if err := RestoreDisabledRuntimeAccounts(); err != nil {
		t.Fatalf("RestoreDisabledRuntimeAccounts() error = %v", err)
	}
}

func TestRestoreDisabledRuntimeAccountsRefusesReplacedAccountsLikeRust(t *testing.T) {
	alias := "C:\\managed\\alias.exe"
	record := &windowssandbox.InstallationRecord{
		Runtime: &windowssandbox.RuntimeRegistration{
			PackageFamily: "OpenAI.Codex_2p2nqsd0c76g0",
			Accounts: []windowssandbox.RuntimeAccountRegistration{{
				Account:             windowssandbox.SandboxRuntimeAccountOffline,
				UserSID:             "S-1-5-21-1-2-3-1001",
				AliasPath:           &alias,
				CleanupLogonPending: true,
			}},
		},
	}
	useRemovalSeams(t, record, nil, "OpenAI.Codex_2p2nqsd0c76g0", 0, true, func(string) (string, error) {
		return "S-1-5-21-1-2-3-9999", nil
	})
	err := RestoreDisabledRuntimeAccounts()
	if err == nil || err.Error() != "managed runtime account was replaced before cleanup" {
		t.Fatalf("RestoreDisabledRuntimeAccounts(replaced) error = %v", err)
	}
}

func TestRestoreDisabledRuntimeAccountsNeedsTheAccountLikeRust(t *testing.T) {
	alias := "C:\\managed\\alias.exe"
	record := &windowssandbox.InstallationRecord{
		Runtime: &windowssandbox.RuntimeRegistration{
			PackageFamily: "OpenAI.Codex_2p2nqsd0c76g0",
			Accounts: []windowssandbox.RuntimeAccountRegistration{{
				Account:             windowssandbox.SandboxRuntimeAccountOffline,
				UserSID:             "S-1-5-21-1-2-3-1001",
				AliasPath:           &alias,
				CleanupLogonPending: true,
			}},
		},
	}
	useRemovalSeams(t, record, nil, "OpenAI.Codex_2p2nqsd0c76g0", 0, false, func(string) (string, error) {
		return "S-1-5-21-1-2-3-1001", nil
	})
	err := RestoreDisabledRuntimeAccounts()
	if err == nil || err.Error() != "cleanup account disappeared before flag restoration" {
		t.Fatalf("RestoreDisabledRuntimeAccounts(missing account) error = %v", err)
	}
}

func TestPrepareRuntimeRemovalRefusesARetiringRuntimeLikeRust(t *testing.T) {
	generation := "generation-a"
	record := &windowssandbox.InstallationRecord{
		Runtime: &windowssandbox.RuntimeRegistration{Retiring: &generation},
	}
	if _, err := PrepareRuntimeRemoval(windows.Token(0), record); err == nil || err.Error() != "runtime is already retiring" {
		t.Fatalf("PrepareRuntimeRemoval(retiring) error = %v", err)
	}
}

func TestPrepareRuntimeRemovalRefusesMissingAccountWithRegisteredPackageLikeRust(t *testing.T) {
	alias := "C:\\managed\\alias.exe"
	record := &windowssandbox.InstallationRecord{
		CodexHome: t.TempDir(),
		Runtime: &windowssandbox.RuntimeRegistration{
			PackageFamily: "OpenAI.Codex_2p2nqsd0c76g0",
			Accounts: []windowssandbox.RuntimeAccountRegistration{{
				Account:   windowssandbox.SandboxRuntimeAccountOffline,
				UserSID:   "S-1-5-21-1-2-3-1001",
				AliasPath: &alias,
			}},
		},
	}
	previousFlags := runtimeLocalUserFlags
	previousPackages := runtimeRegisteredPackages
	t.Cleanup(func() {
		runtimeLocalUserFlags = previousFlags
		runtimeRegisteredPackages = previousPackages
	})
	runtimeLocalUserFlags = func(string) (uint32, bool, error) { return 0, false, nil }
	runtimeRegisteredPackages = func(string, string) ([]string, error) {
		return []string{"OpenAI.Codex_2p2nqsd0c76g0_1.0.0.0_x64__abc"}, nil
	}
	_, err := PrepareRuntimeRemoval(windows.Token(0), record)
	if err == nil || err.Error() != "runtime account is missing while its package remains registered" {
		t.Fatalf("PrepareRuntimeRemoval(missing account) error = %v", err)
	}
}
