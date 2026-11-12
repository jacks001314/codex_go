//go:build windows

package windowsandboxservice

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

func TestRegistrationResultCheckReportsSuccessLikeRust(t *testing.T) {
	result := &registrationResult{
		status:  asyncStatusCompleted,
		details: &deploymentDetails{activityID: packageManagerIID},
	}
	if err := result.check(nil); err != nil {
		t.Fatalf("check(success) error = %v", err)
	}
}

func TestRegistrationResultCheckReportsDeploymentFailureLikeRust(t *testing.T) {
	result := &registrationResult{
		status:  asyncStatusError,
		details: &deploymentDetails{extendedError: 0x80073CF3, activityID: packageManagerIID},
	}
	err := result.check(nil)
	if err == nil {
		t.Fatal("check(deployment failure) = nil")
	}
	for _, want := range []string{
		"finish managed runtime package registration: status=3",
		"async_error=0x00000000",
		"get_results=0x00000000 activity_id=9A7D4B65-5E8F-4FC7-A2E5-7F6925CB8B53",
		"phase=deployment extended_error=0x80073CF3",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("check() error = %v, want %q", err, want)
		}
	}
}

func TestRegistrationResultCheckKeepsGetResultsFailureLikeRust(t *testing.T) {
	result := &registrationResult{
		status:     asyncStatusError,
		detailsErr: hresultError(0x80070005),
	}
	err := result.check(nil)
	if err == nil || !strings.Contains(err.Error(), "phase=GetResults get_results=0x80070005") {
		t.Fatalf("check(get results failure) error = %v", err)
	}
}

func TestRegistrationResultCheckKeepsDiagnosticGetterFailuresLikeRust(t *testing.T) {
	result := &registrationResult{
		status:        asyncStatusError,
		asyncErrorErr: hresultError(0x80004002),
		details: &deploymentDetails{
			extendedErrorErr: hresultError(0x80004001),
			activityIDErr:    hresultError(0x80004003),
		},
	}
	err := result.check(nil)
	if err == nil {
		t.Fatal("check(diagnostic failures) = nil")
	}
	for _, want := range []string{
		"async_error=unavailable(0x80004002)",
		"activity_id=unavailable(0x80004003)",
		"phase=ExtendedErrorCode getter_error=0x80004001",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("check() error = %v, want %q", err, want)
		}
	}
}

// credentialsRecord builds a record with both managed accounts.
func credentialsRecord(offlineSID string, onlineSID string) *windowssandbox.InstallationRecord {
	alias := "C:\\managed\\alias.exe"
	return &windowssandbox.InstallationRecord{
		UserSID:   "S-1-5-21-1-2-3-1000",
		CodexHome: `C:\Users\owner`,
		Runtime: &windowssandbox.RuntimeRegistration{
			PackageFamily: "OpenAI.Codex_2p2nqsd0c76g0",
			Accounts: []windowssandbox.RuntimeAccountRegistration{
				{Account: windowssandbox.SandboxRuntimeAccountOffline, UserSID: offlineSID, AliasPath: &alias},
				{Account: windowssandbox.SandboxRuntimeAccountOnline, UserSID: onlineSID, AliasPath: &alias},
			},
		},
	}
}

func useCredentialsSeams(t *testing.T, resolveSID func(string) (string, error), packages func(string, string) ([]string, error)) {
	t.Helper()
	previousResolve := runtimeResolveSIDString
	previousPackages := runtimeRegisteredPackages
	previousGroups := runtimeTokenGroupSIDs
	previousUserSID := runtimeUserSIDString
	t.Cleanup(func() {
		runtimeResolveSIDString = previousResolve
		runtimeRegisteredPackages = previousPackages
		runtimeTokenGroupSIDs = previousGroups
		runtimeUserSIDString = previousUserSID
	})
	runtimeResolveSIDString = resolveSID
	runtimeRegisteredPackages = packages
	runtimeTokenGroupSIDs = func(windows.Token, uint32) ([]runtimeGroup, error) {
		return []runtimeGroup{{SID: "S-1-5-21-9-9-9-513", Attributes: windows.SE_GROUP_ENABLED}}, nil
	}
	runtimeUserSIDString = func(token windows.Token) (string, error) {
		if token == 1 {
			return "S-1-5-21-1-2-3-1001", nil
		}
		return "S-1-5-21-1-2-3-1002", nil
	}
}

func TestCredentialsNeedRepairAcceptsMatchingRecordedAccountsLikeRust(t *testing.T) {
	useCredentialsSeams(t, func(name string) (string, error) {
		if name == runtimeSandboxUsersGroup {
			return "S-1-5-21-9-9-9-513", nil
		}
		if name == windowssandbox.OnlineUsername {
			return "S-1-5-21-1-2-3-1002", nil
		}
		return "S-1-5-21-1-2-3-1001", nil
	}, func(string, string) ([]string, error) {
		return nil, errors.New("packages must not be queried for recorded accounts")
	})
	record := credentialsRecord("S-1-5-21-1-2-3-1001", "S-1-5-21-1-2-3-1002")
	repair, err := CredentialsNeedRepair(record, func(account windowssandbox.SandboxRuntimeAccount) (windows.Token, error) {
		if account == windowssandbox.SandboxRuntimeAccountOffline {
			return windows.Token(1), nil
		}
		return windows.Token(2), nil
	})
	if err != nil || repair {
		t.Fatalf("CredentialsNeedRepair() = %v, %v", repair, err)
	}
}

func TestCredentialsNeedRepairRequestsSetupForMismatchedCredentialsLikeRust(t *testing.T) {
	useCredentialsSeams(t, func(name string) (string, error) {
		if name == runtimeSandboxUsersGroup {
			return "S-1-5-21-9-9-9-513", nil
		}
		if name == windowssandbox.OnlineUsername {
			return "S-1-5-21-1-2-3-1002", nil
		}
		return "S-1-5-21-1-2-3-1001", nil
	}, func(string, string) ([]string, error) {
		return nil, errors.New("recorded accounts never inspect packages")
	})
	record := credentialsRecord("S-1-5-21-1-2-3-1001", "S-1-5-21-1-2-3-1002")
	repair, err := CredentialsNeedRepair(record, func(account windowssandbox.SandboxRuntimeAccount) (windows.Token, error) {
		if account == windowssandbox.SandboxRuntimeAccountOffline {
			return 0, &windowssandbox.SandboxAccountCredentialMismatch{Underlying: errors.New("bad password")}
		}
		return windows.Token(2), nil
	})
	if err != nil || !repair {
		t.Fatalf("CredentialsNeedRepair(mismatch) = %v, %v", repair, err)
	}
}

func TestCredentialsNeedRepairRefusesReplacedAccountsLikeRust(t *testing.T) {
	useCredentialsSeams(t, func(name string) (string, error) {
		if name == runtimeSandboxUsersGroup {
			return "S-1-5-21-9-9-9-513", nil
		}
		return "S-1-5-21-9-9-9-7777", nil
	}, func(string, string) ([]string, error) {
		return nil, errors.New("recorded accounts never inspect packages")
	})
	record := credentialsRecord("S-1-5-21-1-2-3-1001", "S-1-5-21-1-2-3-1002")
	_, err := CredentialsNeedRepair(record, func(windowssandbox.SandboxRuntimeAccount) (windows.Token, error) {
		return 0, &windowssandbox.SandboxAccountCredentialMismatch{Underlying: errors.New("bad password")}
	})
	if err == nil || err.Error() != "managed runtime account was replaced" {
		t.Fatalf("CredentialsNeedRepair(replaced) error = %v", err)
	}
}

func TestCredentialsNeedRepairRefusesUnrecordedRegistrationsLikeRust(t *testing.T) {
	useCredentialsSeams(t, func(name string) (string, error) {
		if name == runtimeSandboxUsersGroup {
			return "S-1-5-21-9-9-9-513", nil
		}
		if name == windowssandbox.OnlineUsername {
			return "S-1-5-21-1-2-3-1002", nil
		}
		return "S-1-5-21-1-2-3-1001", nil
	}, func(sid string, family string) ([]string, error) {
		return []string{"OpenAI.Codex_2p2nqsd0c76g0_1.0.0.0_x64__abc"}, nil
	})
	// The initial migration has no recorded account SIDs yet.
	record := credentialsRecord("", "")
	_, err := CredentialsNeedRepair(record, func(windowssandbox.SandboxRuntimeAccount) (windows.Token, error) {
		return 0, &windowssandbox.SandboxAccountCredentialMismatch{Underlying: errors.New("bad password")}
	})
	if err == nil || err.Error() != "sandbox account has a runtime registration not owned by this service" {
		t.Fatalf("CredentialsNeedRepair(foreign registration) error = %v", err)
	}
}

func TestCredentialsNeedRepairPropagatesOtherLogonFailuresLikeRust(t *testing.T) {
	useCredentialsSeams(t, func(name string) (string, error) {
		return "S-1-5-21-1-2-3-1001", nil
	}, func(string, string) ([]string, error) { return nil, nil })
	failure := errors.New("logon service unavailable")
	record := credentialsRecord("S-1-5-21-1-2-3-1001", "S-1-5-21-1-2-3-1002")
	if _, err := CredentialsNeedRepair(record, func(windowssandbox.SandboxRuntimeAccount) (windows.Token, error) {
		return 0, failure
	}); !errors.Is(err, failure) {
		t.Fatalf("CredentialsNeedRepair(logon failure) error = %v", err)
	}
}

// TestRegistrationResultReconcilesOnlyServicePrivilegeErrorsLikeRust pins Rust
// #60944: only a terminal service-privilege error may succeed through the
// target-registration check, and only when that check reports the exact
// postcondition.
func TestRegistrationResultReconcilesOnlyServicePrivilegeErrorsLikeRust(t *testing.T) {
	const accessDenied = 0x80070005
	cases := []struct {
		name       string
		status     int32
		code       uint32
		registered bool
		wantOK     bool
		wantCheck  bool
	}{
		{"reconciled", asyncStatusError, errorPackagedServiceRequiresAdminPrivileges, true, true, true},
		{"missing target registration", asyncStatusError, errorPackagedServiceRequiresAdminPrivileges, false, false, true},
		{"canceled", asyncStatusCanceled, errorPackagedServiceRequiresAdminPrivileges, true, false, false},
		{"unrelated access denied", asyncStatusError, accessDenied, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checked := false
			result := &registrationResult{
				status:  tc.status,
				details: &deploymentDetails{extendedError: tc.code, activityID: packageManagerIID},
			}
			err := result.check(func() bool {
				checked = true
				return tc.registered
			})
			if checked != tc.wantCheck {
				t.Fatalf("registration check called = %v, want %v", checked, tc.wantCheck)
			}
			if (err == nil) != tc.wantOK {
				t.Fatalf("check() error = %v, wantOK %v", err, tc.wantOK)
			}
		})
	}
}
