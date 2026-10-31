//go:build windows

package windowssandbox

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// TestLogonExistingSandboxAccountRequiresMarkerLikeRust refuses to log on
// before any setup marker exists, without creating files under the home.
func TestLogonExistingSandboxAccountRequiresMarkerLikeRust(t *testing.T) {
	home := t.TempDir()
	_, err := LogonExistingSandboxAccount(home, SandboxRuntimeAccountOffline)
	if err == nil || !strings.Contains(err.Error(), "read sandbox setup marker") || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("LogonExistingSandboxAccount(no marker) error = %v", err)
	}
	if _, statErr := os.Stat(SetupMarkerPath(home)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("a failed logon created a setup marker: %v", statErr)
	}
}

// TestLogonExistingSandboxAccountRefusesIncompatibleMarkerLikeRust rejects a
// marker written by another setup version.
func TestLogonExistingSandboxAccountRefusesIncompatibleMarkerLikeRust(t *testing.T) {
	home := t.TempDir()
	marker := &SetupMarker{Version: SetupVersion + 1}
	if err := WriteSetupMarker(home, marker); err != nil {
		t.Fatalf("WriteSetupMarker() error = %v", err)
	}
	_, err := LogonExistingSandboxAccount(home, SandboxRuntimeAccountOffline)
	if err == nil || err.Error() != "sandbox setup marker is missing or incompatible" {
		t.Fatalf("LogonExistingSandboxAccount(incompatible marker) error = %v", err)
	}
}

// TestLogonExistingSandboxAccountRefusesAnotherAccountLikeRust never logs on
// with a credential record that names a different account.
func TestLogonExistingSandboxAccountRefusesAnotherAccountLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := WriteSetupMarker(home, &SetupMarker{Version: SetupVersion}); err != nil {
		t.Fatalf("WriteSetupMarker() error = %v", err)
	}
	users := &SandboxUsersFile{
		Version: SetupVersion,
		Offline: SandboxUserRecord{Username: "not-the-managed-account", Password: "AAAA"},
		Online:  SandboxUserRecord{Username: OnlineUsername, Password: "AAAA"},
	}
	if err := WriteSandboxUsersFile(home, users); err != nil {
		t.Fatalf("WriteSandboxUsersFile() error = %v", err)
	}
	_, err := LogonExistingSandboxAccount(home, SandboxRuntimeAccountOffline)
	if err == nil || err.Error() != "sandbox account record does not match the managed account" {
		t.Fatalf("LogonExistingSandboxAccount(other account) error = %v", err)
	}
}

// TestLogonExistingSandboxAccountRefusesIncompatibleAccountsLikeRust rejects
// credentials written by another setup version.
func TestLogonExistingSandboxAccountRefusesIncompatibleAccountsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := WriteSetupMarker(home, &SetupMarker{Version: SetupVersion}); err != nil {
		t.Fatalf("WriteSetupMarker() error = %v", err)
	}
	users := &SandboxUsersFile{
		Version: SetupVersion + 1,
		Offline: SandboxUserRecord{Username: OfflineUsername, Password: "AAAA"},
		Online:  SandboxUserRecord{Username: OnlineUsername, Password: "AAAA"},
	}
	if err := WriteSandboxUsersFile(home, users); err != nil {
		t.Fatalf("WriteSandboxUsersFile() error = %v", err)
	}
	_, err := LogonExistingSandboxAccount(home, SandboxRuntimeAccountOffline)
	if err == nil || err.Error() != "sandbox accounts are incompatible" {
		t.Fatalf("LogonExistingSandboxAccount(incompatible accounts) error = %v", err)
	}
}
