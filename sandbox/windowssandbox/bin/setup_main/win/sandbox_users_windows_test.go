//go:build windows

package win

import (
	"testing"

	"codex_go/sandbox/windowssandbox"
)

func TestResolveWellKnownSID(t *testing.T) {
	sid, err := ResolveSID("Everyone")
	if err != nil {
		t.Fatalf("ResolveSID(Everyone) error = %v", err)
	}
	if got := windowssandbox.StringFromSIDBytes(sid); got != "S-1-1-0" {
		t.Fatalf("SID = %q", got)
	}
	name, err := LookupAccountNameForSID("S-1-5-32-545")
	if err != nil {
		t.Fatalf("LookupAccountNameForSID(Users) error = %v", err)
	}
	if name == "" {
		t.Fatalf("Users SID resolved to empty name")
	}
}

func TestWriteSandboxUserSecrets(t *testing.T) {
	home := t.TempDir()
	if err := WriteSandboxUserSecrets(home, "offline", "offline-password", "online", "online-password"); err != nil {
		t.Fatalf("WriteSandboxUserSecrets() error = %v", err)
	}
	users, err := windowssandbox.ReadSandboxUsersFile(home)
	if err != nil {
		t.Fatalf("ReadSandboxUsersFile() error = %v", err)
	}
	if users.Offline.Username != "offline" || users.Online.Username != "online" {
		t.Fatalf("users = %#v", users)
	}
	offline, err := windowssandbox.DecodeSandboxUserPassword(users.Offline)
	if err != nil {
		t.Fatalf("DecodeSandboxUserPassword(offline) error = %v", err)
	}
	if offline != "offline-password" {
		t.Fatalf("offline password = %q", offline)
	}
}
