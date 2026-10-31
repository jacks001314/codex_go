//go:build windows

package windowsandboxservice

import (
	"testing"

	"codex_go/sandbox/windowssandbox"
)

// refreshRecord builds a resumable registered record for the refresh gate.
func refreshRecord(identity *ClientIdentity, family string) (*windowssandbox.InstallationRecord, *windowssandbox.RuntimeRegistration) {
	alias := "C:\\managed\\alias.exe"
	runtime := &windowssandbox.RuntimeRegistration{
		PackageFamily: family,
		Accounts: []windowssandbox.RuntimeAccountRegistration{
			{Account: windowssandbox.SandboxRuntimeAccountOffline, UserSID: "S-1-5-21-1-2-3-1001", AliasPath: &alias},
			{Account: windowssandbox.SandboxRuntimeAccountOnline, UserSID: "S-1-5-21-1-2-3-1002", AliasPath: &alias},
		},
	}
	record := &windowssandbox.InstallationRecord{
		UserSID:   identity.UserSID,
		CodexHome: identity.CodexHome,
		Runtime:   runtime,
	}
	return record, runtime
}

func useRefreshSeams(t *testing.T, family string, flags uint32, found bool, resolved map[string]string) {
	t.Helper()
	previousFamily := runtimeProcessPackageFamily
	previousFlags := runtimeLocalUserFlags
	previousResolve := runtimeResolveSIDString
	t.Cleanup(func() {
		runtimeProcessPackageFamily = previousFamily
		runtimeLocalUserFlags = previousFlags
		runtimeResolveSIDString = previousResolve
	})
	runtimeProcessPackageFamily = func() (string, bool, error) { return family, true, nil }
	runtimeLocalUserFlags = func(string) (uint32, bool, error) { return flags, found, nil }
	runtimeResolveSIDString = func(name string) (string, error) {
		if sid, ok := resolved[name]; ok {
			return sid, nil
		}
		return "", nil
	}
}

func TestCheckRegistrationRefreshAdmissionAcceptsUnchangedOwnershipLikeRust(t *testing.T) {
	identity := testIdentity(t, `C:\Users\owner`)
	record, runtime := refreshRecord(identity, "OpenAI.Codex_2p2nqsd0c76g0")
	useRefreshSeams(t, "OpenAI.Codex_2p2nqsd0c76g0", 0x10, true, map[string]string{
		windowssandbox.OfflineUsername: "S-1-5-21-1-2-3-1001",
		windowssandbox.OnlineUsername:  "S-1-5-21-1-2-3-1002",
	})
	if err := checkRegistrationRefreshAdmission(record, runtime, identity, true); err != nil {
		t.Fatalf("checkRegistrationRefreshAdmission() error = %v", err)
	}
}

func TestCheckRegistrationRefreshAdmissionRefusesChangedOwnershipLikeRust(t *testing.T) {
	identity := testIdentity(t, `C:\Users\owner`)
	useRefreshSeams(t, "OpenAI.Codex_2p2nqsd0c76g0", 0x10, true, map[string]string{
		windowssandbox.OfflineUsername: "S-1-5-21-1-2-3-1001",
		windowssandbox.OnlineUsername:  "S-1-5-21-1-2-3-1002",
	})
	cases := []struct {
		name          string
		mutate        func(record *windowssandbox.InstallationRecord, runtime *windowssandbox.RuntimeRegistration)
		setupComplete bool
	}{
		{name: "other owner", mutate: func(record *windowssandbox.InstallationRecord, runtime *windowssandbox.RuntimeRegistration) {
			record.UserSID = "S-1-5-21-1-2-3-9999"
		}, setupComplete: true},
		{name: "other home", mutate: func(record *windowssandbox.InstallationRecord, runtime *windowssandbox.RuntimeRegistration) {
			record.CodexHome = `C:\Users\other`
		}, setupComplete: true},
		{name: "other package", mutate: func(record *windowssandbox.InstallationRecord, runtime *windowssandbox.RuntimeRegistration) {
			runtime.PackageFamily = "OpenAI.Codex_0000000000000"
		}, setupComplete: true},
		{name: "retiring", mutate: func(record *windowssandbox.InstallationRecord, runtime *windowssandbox.RuntimeRegistration) {
			generation := "generation-a"
			runtime.Retiring = &generation
		}, setupComplete: true},
		{name: "incomplete setup", mutate: func(record *windowssandbox.InstallationRecord, runtime *windowssandbox.RuntimeRegistration) {}, setupComplete: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			record, runtime := refreshRecord(identity, "OpenAI.Codex_2p2nqsd0c76g0")
			testCase.mutate(record, runtime)
			err := checkRegistrationRefreshAdmission(record, runtime, identity, testCase.setupComplete)
			if err == nil || err.Error() != "registration refresh requires unchanged sandbox ownership and settings" {
				t.Fatalf("checkRegistrationRefreshAdmission() error = %v", err)
			}
		})
	}
}

func TestCheckRegistrationRefreshAdmissionRefusesBrokenAccountsLikeRust(t *testing.T) {
	identity := testIdentity(t, `C:\Users\owner`)
	record, runtime := refreshRecord(identity, "OpenAI.Codex_2p2nqsd0c76g0")
	useRefreshSeams(t, "OpenAI.Codex_2p2nqsd0c76g0", userFlagAccountDisable, true, map[string]string{
		windowssandbox.OfflineUsername: "S-1-5-21-1-2-3-1001",
		windowssandbox.OnlineUsername:  "S-1-5-21-1-2-3-1002",
	})
	err := checkRegistrationRefreshAdmission(record, runtime, identity, true)
	if err == nil || err.Error() != "registration refresh cannot repair or replace a sandbox account" {
		t.Fatalf("checkRegistrationRefreshAdmission(disabled) error = %v", err)
	}
	useRefreshSeams(t, "OpenAI.Codex_2p2nqsd0c76g0", 0x10, true, map[string]string{
		windowssandbox.OfflineUsername: "S-1-5-21-1-2-3-7777",
		windowssandbox.OnlineUsername:  "S-1-5-21-1-2-3-1002",
	})
	err = checkRegistrationRefreshAdmission(record, runtime, identity, true)
	if err == nil || err.Error() != "registration refresh cannot repair or replace a sandbox account" {
		t.Fatalf("checkRegistrationRefreshAdmission(replaced) error = %v", err)
	}
}
