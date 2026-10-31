//go:build windows

package windowsandboxservice

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

func currentUserSID(t *testing.T) string {
	t.Helper()
	sid, err := tokenUserSIDBytes(openTestToken(t))
	if err != nil {
		t.Fatalf("read the current user SID: %v", err)
	}
	return windowssandbox.StringFromSIDBytes(sid)
}

func stubSessionToken(t *testing.T, token windows.Token, err error) {
	t.Helper()
	previous := querySessionUserToken
	querySessionUserToken = func(uint32) (windows.Token, error) { return token, err }
	t.Cleanup(func() { querySessionUserToken = previous })
}

func stubWindowsSessions(t *testing.T, sessionIDs []uint32, err error) {
	t.Helper()
	previous := listWindowsSessions
	listWindowsSessions = func() ([]uint32, error) { return sessionIDs, err }
	t.Cleanup(func() { listWindowsSessions = previous })
}

func TestRestoreAuthenticatedUserSkipsWithoutARecord(t *testing.T) {
	queried := false
	previous := querySessionUserToken
	querySessionUserToken = func(uint32) (windows.Token, error) {
		queried = true
		return 0, errors.New("no session token expected")
	}
	t.Cleanup(func() { querySessionUserToken = previous })
	lifecycle := &Lifecycle{loadRecord: func() (*windowssandbox.InstallationRecord, error) { return nil, nil }}
	if err := lifecycle.RestoreAuthenticatedUser(4); err != nil {
		t.Fatalf("RestoreAuthenticatedUser() error = %v", err)
	}
	if queried {
		t.Fatal("RestoreAuthenticatedUser() opened a session token without a record")
	}
}

func TestRestoreAuthenticatedUserSkipsARetainedInstallation(t *testing.T) {
	loaded := false
	lifecycle := &Lifecycle{
		subscribe:    noUninstallSubscription,
		installation: &lifecycleInstallation{record: &windowssandbox.InstallationRecord{UserSID: "S-1-5-21-1"}, codexHome: "C:\\Users\\owner"},
		loadRecord: func() (*windowssandbox.InstallationRecord, error) {
			loaded = true
			return nil, nil
		},
	}
	if err := lifecycle.RestoreAuthenticatedUser(4); err != nil {
		t.Fatalf("RestoreAuthenticatedUser() error = %v", err)
	}
	if loaded {
		t.Fatal("RestoreAuthenticatedUser() reloaded a retained installation")
	}
}

func TestRestoreAuthenticatedUserRegistersTheRecordedOwner(t *testing.T) {
	home := t.TempDir()
	saved := 0
	lifecycle := &Lifecycle{
		subscribe: noUninstallSubscription,
		loadRecord: func() (*windowssandbox.InstallationRecord, error) {
			return &windowssandbox.InstallationRecord{UserSID: currentUserSID(t), CodexHome: home, SessionID: 2}, nil
		},
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			saved++
			return record, nil
		},
	}
	t.Cleanup(lifecycle.Close)
	token, err := cloneToken(openTestToken(t))
	if err != nil {
		t.Fatalf("cloneToken() error = %v", err)
	}
	stubSessionToken(t, token, nil)
	if err := lifecycle.RestoreAuthenticatedUser(9); err != nil {
		t.Fatalf("RestoreAuthenticatedUser() error = %v", err)
	}
	if saved != 1 {
		t.Fatalf("RestoreAuthenticatedUser() saved %d records, want 1", saved)
	}
	if lifecycle.CodexHome() != home {
		t.Fatalf("CodexHome() = %q, want %q", lifecycle.CodexHome(), home)
	}
	lifecycle.mu.Lock()
	sessionID := lifecycle.installation.record.SessionID
	lifecycle.mu.Unlock()
	if sessionID != 9 {
		t.Fatalf("recorded session = %d, want 9", sessionID)
	}
}

func TestRestoreAuthenticatedUserRefusesADifferentLoggedInUser(t *testing.T) {
	saved := 0
	lifecycle := &Lifecycle{
		subscribe: noUninstallSubscription,
		loadRecord: func() (*windowssandbox.InstallationRecord, error) {
			return &windowssandbox.InstallationRecord{UserSID: "S-1-5-21-9-9-9", CodexHome: t.TempDir()}, nil
		},
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			saved++
			return record, nil
		},
	}
	t.Cleanup(lifecycle.Close)
	token, err := cloneToken(openTestToken(t))
	if err != nil {
		t.Fatalf("cloneToken() error = %v", err)
	}
	stubSessionToken(t, token, nil)
	err = lifecycle.RestoreAuthenticatedUser(3)
	if err == nil || !strings.Contains(err.Error(), "does not match the recorded sandbox owner") {
		t.Fatalf("RestoreAuthenticatedUser() error = %v", err)
	}
	if saved != 0 {
		t.Fatalf("RestoreAuthenticatedUser() saved %d records, want 0", saved)
	}
}

func TestRestoreAuthenticatedUserSkipsAnotherPackageFamily(t *testing.T) {
	if _, present, err := provisionservice.ProcessPackageFamily(windows.CurrentProcess()); err != nil || present {
		t.Skip("the test process runs with an installed package identity")
	}
	queried := false
	previous := querySessionUserToken
	querySessionUserToken = func(uint32) (windows.Token, error) {
		queried = true
		return 0, errors.New("no session token expected")
	}
	t.Cleanup(func() { querySessionUserToken = previous })
	lifecycle := &Lifecycle{
		subscribe: noUninstallSubscription,
		loadRecord: func() (*windowssandbox.InstallationRecord, error) {
			return &windowssandbox.InstallationRecord{
				UserSID: "S-1-5-21-1",
				Runtime: &windowssandbox.RuntimeRegistration{PackageFamily: "OpenAI.Codex_123"},
			}, nil
		},
		saveRecord: func(*windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			t.Fatal("RestoreAuthenticatedUser() saved a record from another package family")
			return nil, nil
		},
	}
	if err := lifecycle.RestoreAuthenticatedUser(1); err != nil {
		t.Fatalf("RestoreAuthenticatedUser() error = %v", err)
	}
	if queried {
		t.Fatal("RestoreAuthenticatedUser() opened a session token for another package family")
	}
}

func TestRestoreRecordedOwnerFallsBackToAnotherSession(t *testing.T) {
	home := t.TempDir()
	saves := 0
	lifecycle := &Lifecycle{
		subscribe: noUninstallSubscription,
		loadRecord: func() (*windowssandbox.InstallationRecord, error) {
			return &windowssandbox.InstallationRecord{UserSID: currentUserSID(t), CodexHome: home, SessionID: 3}, nil
		},
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			saves++
			return record, nil
		},
	}
	t.Cleanup(lifecycle.Close)
	token, err := cloneToken(openTestToken(t))
	if err != nil {
		t.Fatalf("cloneToken() error = %v", err)
	}
	previous := querySessionUserToken
	querySessionUserToken = func(sessionID uint32) (windows.Token, error) {
		if sessionID == 3 {
			return 0, errors.New("the recorded session is gone")
		}
		return token, nil
	}
	t.Cleanup(func() { querySessionUserToken = previous })
	stubWindowsSessions(t, []uint32{3, 5}, nil)
	if err := lifecycle.RestoreRecordedOwner(); err != nil {
		t.Fatalf("RestoreRecordedOwner() error = %v", err)
	}
	if saves != 1 {
		t.Fatalf("RestoreRecordedOwner() saved %d records, want 1", saves)
	}
}

func TestRestoreRecordedOwnerReportsTheRecordedFailure(t *testing.T) {
	lifecycle := &Lifecycle{
		subscribe: noUninstallSubscription,
		loadRecord: func() (*windowssandbox.InstallationRecord, error) {
			return &windowssandbox.InstallationRecord{UserSID: currentUserSID(t), CodexHome: t.TempDir(), SessionID: 3}, nil
		},
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			return record, nil
		},
	}
	t.Cleanup(lifecycle.Close)
	stubSessionToken(t, 0, errors.New("no logged-in user"))
	stubWindowsSessions(t, []uint32{3, 5}, nil)
	err := lifecycle.RestoreRecordedOwner()
	if err == nil || !strings.Contains(err.Error(), "no logged-in user") {
		t.Fatalf("RestoreRecordedOwner() error = %v", err)
	}
}
