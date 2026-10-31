//go:build windows

package windowsandboxservice

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

func openTestToken(t *testing.T) windows.Token {
	t.Helper()
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE, &token); err != nil {
		t.Fatalf("OpenProcessToken() error = %v", err)
	}
	t.Cleanup(func() { _ = token.Close() })
	return token
}

func TestLifecycleRefusesADifferentOwnerOrHome(t *testing.T) {
	previous := &windowssandbox.InstallationRecord{
		UserSID:   "S-1-5-21-1-2-3-1000",
		CodexHome: "C:\\Users\\owner\\.codex",
	}
	saved := 0
	lifecycle := &Lifecycle{
		loadRecord: func() (*windowssandbox.InstallationRecord, error) { return previous, nil },
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			saved++
			return record, nil
		},
	}
	_, err := lifecycle.RegisterOwner(&windowssandbox.InstallationRecord{
		UserSID:   "S-1-5-21-9-9-9",
		CodexHome: previous.CodexHome,
	}, openTestToken(t), windowssandbox.SetupRuntimeLegacy)
	if err == nil || !IsServiceUnavailable(err) {
		t.Fatalf("RegisterOwner() error = %v, want a ServiceUnavailable refusal", err)
	}
	if !strings.Contains(err.Error(), "different owner or home") {
		t.Fatalf("RegisterOwner() error = %v", err)
	}
	if saved != 0 {
		t.Fatalf("RegisterOwner() saved %d times, want 0", saved)
	}
}

func TestLifecycleMergesDesktopOwnershipAndPinsTheHome(t *testing.T) {
	home := t.TempDir()
	previous := &windowssandbox.InstallationRecord{
		UserSID:             "S-1-5-21-1-2-3-1000",
		CodexHome:           home,
		DesktopInstallation: &windowssandbox.DesktopInstallation{CreatedCodexHome: true, CacheHome: home + "-cache"},
	}
	saved := 0
	lifecycle := &Lifecycle{
		loadRecord: func() (*windowssandbox.InstallationRecord, error) { return previous, nil },
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			saved++
			return record, nil
		},
	}
	t.Cleanup(lifecycle.Close)
	got, err := lifecycle.RegisterOwner(&windowssandbox.InstallationRecord{
		UserSID:   previous.UserSID,
		CodexHome: home,
		SessionID: 3,
	}, openTestToken(t), windowssandbox.SetupRuntimeLegacy)
	if err != nil {
		t.Fatalf("RegisterOwner() error = %v", err)
	}
	if saved != 1 {
		t.Fatalf("RegisterOwner() saved %d times, want 1", saved)
	}
	if got.DesktopInstallation == nil || !got.DesktopInstallation.CreatedCodexHome {
		t.Fatalf("RegisterOwner() dropped desktop ownership: %+v", got.DesktopInstallation)
	}
	if lifecycle.CodexHome() != home {
		t.Fatalf("CodexHome() = %q, want %q", lifecycle.CodexHome(), home)
	}
}

func TestLifecycleReusesARetainedInstallation(t *testing.T) {
	home := t.TempDir()
	saves := 0
	lifecycle := &Lifecycle{
		loadRecord: func() (*windowssandbox.InstallationRecord, error) { return nil, nil },
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			saves++
			return record, nil
		},
	}
	t.Cleanup(lifecycle.Close)
	owner := "S-1-5-21-1-2-3-1000"
	if _, err := lifecycle.RegisterOwner(&windowssandbox.InstallationRecord{
		UserSID:   owner,
		CodexHome: home,
		SessionID: 1,
	}, openTestToken(t), windowssandbox.SetupRuntimeLegacy); err != nil {
		t.Fatalf("first RegisterOwner() error = %v", err)
	}
	if _, err := lifecycle.RegisterOwner(&windowssandbox.InstallationRecord{
		UserSID:   owner,
		CodexHome: home,
		SessionID: 2,
	}, openTestToken(t), windowssandbox.SetupRuntimeLegacy); err != nil {
		t.Fatalf("second RegisterOwner() error = %v", err)
	}
	if saves != 2 {
		t.Fatalf("RegisterOwner() saved %d times, want 2", saves)
	}
	if lifecycle.CodexHome() != home {
		t.Fatalf("CodexHome() = %q, want %q", lifecycle.CodexHome(), home)
	}
}

func TestLifecycleSkipsCleanupWhenTheHomeCannotBePinned(t *testing.T) {
	saves := 0
	lifecycle := &Lifecycle{
		loadRecord: func() (*windowssandbox.InstallationRecord, error) { return nil, nil },
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			saves++
			return record, nil
		},
	}
	t.Cleanup(lifecycle.Close)
	missing := t.TempDir() + "\\does-not-exist"
	got, err := lifecycle.RegisterOwner(&windowssandbox.InstallationRecord{
		UserSID:   "S-1-5-21-1-2-3-1000",
		CodexHome: missing,
	}, openTestToken(t), windowssandbox.SetupRuntimeLegacy)
	if err != nil {
		t.Fatalf("RegisterOwner() error = %v", err)
	}
	if saves != 1 || got == nil {
		t.Fatalf("RegisterOwner() saved %d times, record %+v", saves, got)
	}
	if lifecycle.CodexHome() != "" {
		t.Fatalf("CodexHome() = %q, want an unpinned home", lifecycle.CodexHome())
	}
}

func TestLifecycleRequiresAnOwnerToken(t *testing.T) {
	lifecycle := &Lifecycle{
		loadRecord: func() (*windowssandbox.InstallationRecord, error) { return nil, nil },
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			return record, nil
		},
	}
	_, err := lifecycle.RegisterOwner(&windowssandbox.InstallationRecord{UserSID: "S-1-5-21-1"}, 0, windowssandbox.SetupRuntimeLegacy)
	if err == nil {
		t.Fatalf("RegisterOwner() error = %v", err)
	}
}

func TestLifecycleServesRegistrationRequests(t *testing.T) {
	home := t.TempDir()
	saved := 0
	lifecycle := &Lifecycle{
		loadRecord: func() (*windowssandbox.InstallationRecord, error) { return nil, nil },
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			saved++
			return record, nil
		},
	}
	t.Cleanup(lifecycle.Close)
	identity := &ClientIdentity{
		Token:     openTestToken(t),
		UserSID:   "S-1-5-21-4-5-6-1100",
		CodexHome: home,
		SessionID: 4,
	}
	response, err := lifecycle.ServeProvisioningRequest(identity, &ServiceRequest{
		Register: &RegisterInstallationRequest{CodexHome: home},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("ServeProvisioningRequest() error = %v", err)
	}
	if response.Kind != provisionservice.ResponseOK {
		t.Fatalf("ServeProvisioningRequest() response = %+v, want ok", response)
	}
	if saved != 1 {
		t.Fatalf("lifecycle saved %d records, want 1", saved)
	}
	if lifecycle.CodexHome() != home {
		t.Fatalf("CodexHome() = %q, want %q", lifecycle.CodexHome(), home)
	}
}
