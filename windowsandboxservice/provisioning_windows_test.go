//go:build windows

package windowsandboxservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

func testIdentity(t *testing.T, home string) *ClientIdentity {
	t.Helper()
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE, &token); err != nil {
		t.Fatalf("OpenProcessToken() error = %v", err)
	}
	t.Cleanup(func() { _ = token.Close() })
	return &ClientIdentity{
		Account:   "TEST\\owner",
		CodexHome: home,
		UserSID:   "S-1-5-21-1-2-3-1001",
		SessionID: 1,
		Runtime:   windowssandbox.SetupRuntimeLegacy,
		Token:     token,
	}
}

func TestRunProvisioningRegistersTheOwnerForRegistrationOnly(t *testing.T) {
	identity := testIdentity(t, t.TempDir())
	request := &ServiceRequest{Register: &RegisterInstallationRequest{CodexHome: identity.CodexHome}}
	var recorded *windowssandbox.InstallationRecord
	registrar := func(record *windowssandbox.InstallationRecord, token windows.Token, setupRuntime windowssandbox.SetupRuntime) (*windowssandbox.InstallationRecord, error) {
		recorded = record
		_ = token.Close()
		return record, nil
	}
	response, err := RunProvisioning(identity, request, nil, nil, registrar, nil)
	if err != nil {
		t.Fatalf("RunProvisioning() error = %v", err)
	}
	if response.Kind != provisionservice.ResponseOK {
		t.Fatalf("RunProvisioning() response = %+v, want ok", response)
	}
	if recorded == nil || recorded.UserSID != identity.UserSID || recorded.CodexHome != identity.CodexHome || recorded.SessionID != identity.SessionID {
		t.Fatalf("registered owner = %+v", recorded)
	}
}

func TestRunProvisioningRunsTheRegisteredTransactionUnderTheSetupLock(t *testing.T) {
	identity := testIdentity(t, t.TempDir())
	identity.Runtime = windowssandbox.SetupRuntimeRegistered
	request := &ServiceRequest{Provision: &ProvisioningRequest{CodexHome: identity.CodexHome, RegisteredCore: true}}
	registered := false
	registrar := func(record *windowssandbox.InstallationRecord, token windows.Token, setupRuntime windowssandbox.SetupRuntime) (*windowssandbox.InstallationRecord, error) {
		registered = true
		_ = token.Close()
		return record, nil
	}
	group := "S-1-5-21-9-9-9-513"
	previousResolve := runtimeResolveSIDString
	t.Cleanup(func() { runtimeResolveSIDString = previousResolve })
	runtimeResolveSIDString = func(string) (string, error) { return group, nil }
	sandboxSID, err := windowssandbox.SIDBytesFromString(group)
	if err != nil {
		t.Fatalf("SIDBytesFromString(%s) error = %v", group, err)
	}
	// The transaction acquires the setup lock, checks the sandbox group
	// generation, admits the owner and then refuses an unpackaged service.
	_, err = RunProvisioning(identity, request, sandboxSID, nil, registrar, nil)
	if err == nil || !strings.Contains(err.Error(), "no package identity") {
		t.Fatalf("RunProvisioning(registered) error = %v, want the package-identity refusal", err)
	}
	if !registered {
		t.Fatal("RunProvisioning(registered) never admitted the owner")
	}
}

func TestRunRegisteredProvisioningRefusesAReplacedSandboxGroup(t *testing.T) {
	identity := testIdentity(t, t.TempDir())
	identity.Runtime = windowssandbox.SetupRuntimeRegistered
	request := &ProvisioningRequest{CodexHome: identity.CodexHome, RegisteredCore: true}
	previousResolve := runtimeResolveSIDString
	t.Cleanup(func() { runtimeResolveSIDString = previousResolve })
	runtimeResolveSIDString = func(string) (string, error) { return "S-1-5-21-9-9-9-513", nil }
	stale, err := windowssandbox.SIDBytesFromString("S-1-5-21-9-9-9-9999")
	if err != nil {
		t.Fatalf("SIDBytesFromString() error = %v", err)
	}
	registered := false
	registrar := func(record *windowssandbox.InstallationRecord, token windows.Token, setupRuntime windowssandbox.SetupRuntime) (*windowssandbox.InstallationRecord, error) {
		registered = true
		_ = token.Close()
		return record, nil
	}
	_, err = RunRegisteredProvisioning(identity, request, stale, nil, registrar, nil)
	if err == nil || err.Error() != "sandbox group changed before authentication" {
		t.Fatalf("RunRegisteredProvisioning(stale group) error = %v", err)
	}
	if registered {
		t.Fatal("RunRegisteredProvisioning(stale group) admitted an owner")
	}
}

func TestRunProvisioningRequiresThePackagedSetupHelper(t *testing.T) {
	identity := testIdentity(t, t.TempDir())
	request := &ServiceRequest{Provision: &ProvisioningRequest{CodexHome: identity.CodexHome}}
	registrar := func(record *windowssandbox.InstallationRecord, token windows.Token, setupRuntime windowssandbox.SetupRuntime) (*windowssandbox.InstallationRecord, error) {
		_ = token.Close()
		return record, nil
	}
	restore := executablePath
	t.Cleanup(func() { executablePath = restore })
	executablePath = func() (string, error) {
		return filepath.Join(t.TempDir(), "codex.exe"), nil
	}
	_, err := RunProvisioning(identity, request, nil, nil, registrar, nil)
	if err == nil || !strings.Contains(err.Error(), "inspect packaged setup helper") {
		t.Fatalf("RunProvisioning() error = %v, want the missing-helper refusal", err)
	}
}

func TestRunProvisioningLaunchesTheInjectedSetup(t *testing.T) {
	identity := testIdentity(t, t.TempDir())
	request := &ServiceRequest{Provision: &ProvisioningRequest{CodexHome: identity.CodexHome}}
	registrar := func(record *windowssandbox.InstallationRecord, token windows.Token, setupRuntime windowssandbox.SetupRuntime) (*windowssandbox.InstallationRecord, error) {
		_ = token.Close()
		return record, nil
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, packagedSetupHelperName), []byte("helper"), 0o600); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	restore := executablePath
	t.Cleanup(func() { executablePath = restore })
	executablePath = func() (string, error) {
		return filepath.Join(directory, "codex.exe"), nil
	}
	launched := 0
	setup := func(got *ClientIdentity, provision *ProvisioningRequest) error {
		launched++
		if got != identity || provision.CodexHome != identity.CodexHome {
			t.Fatalf("setup(%+v, %+v) received the wrong request", got, provision)
		}
		return nil
	}
	response, err := RunProvisioning(identity, request, nil, nil, registrar, setup)
	if err != nil {
		t.Fatalf("RunProvisioning() error = %v", err)
	}
	if response.Kind != provisionservice.ResponseOK || launched != 1 {
		t.Fatalf("RunProvisioning() = %+v, launched %d times", response, launched)
	}
}

func TestRunPackagedSetupRequiresThePackagedHelper(t *testing.T) {
	err := RunPackagedSetup(
		&ClientIdentity{CodexHome: t.TempDir(), Account: `DOMAIN\owner`, Token: openTestToken(t)},
		&ProvisioningRequest{},
	)
	if err == nil || !strings.Contains(err.Error(), "packaged setup helper") {
		t.Fatalf("RunPackagedSetup() error = %v", err)
	}
}

func TestRunPackagedSetupRequiresAnAuthenticatedRequest(t *testing.T) {
	if err := RunPackagedSetup(nil, &ProvisioningRequest{}); err == nil {
		t.Fatal("RunPackagedSetup() accepted a missing identity")
	}
	if err := RunPackagedSetup(&ClientIdentity{}, nil); err == nil {
		t.Fatal("RunPackagedSetup() accepted a missing request")
	}
}
