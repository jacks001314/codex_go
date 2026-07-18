//go:build windows

package windowssandbox

import (
	"encoding/base64"
	"testing"
)

func TestSelectSandboxCredentialsDecryptsPassword(t *testing.T) {
	home := t.TempDir()
	if err := WriteSetupMarker(home, &SetupMarker{Version: SetupVersion, OfflineUsername: OfflineUsername, OnlineUsername: OnlineUsername}); err != nil {
		t.Fatalf("WriteSetupMarker() error = %v", err)
	}
	offlineProtected, err := DPAPIProtect([]byte("offline-secret"))
	if err != nil {
		t.Fatalf("DPAPIProtect(offline) error = %v", err)
	}
	onlineProtected, err := DPAPIProtect([]byte("online-secret"))
	if err != nil {
		t.Fatalf("DPAPIProtect(online) error = %v", err)
	}
	if err := WriteSandboxUsersFile(home, &SandboxUsersFile{
		Version: SetupVersion,
		Offline: SandboxUserRecord{
			Username: OfflineUsername,
			Password: base64.StdEncoding.EncodeToString(offlineProtected),
		},
		Online: SandboxUserRecord{
			Username: OnlineUsername,
			Password: base64.StdEncoding.EncodeToString(onlineProtected),
		},
	}); err != nil {
		t.Fatalf("WriteSandboxUsersFile() error = %v", err)
	}
	offline, err := SelectSandboxCredentials(home, SandboxNetworkIdentityOffline)
	if err != nil {
		t.Fatalf("SelectSandboxCredentials(offline) error = %v", err)
	}
	if offline.Username != OfflineUsername || offline.Password != "offline-secret" || offline.Domain != "." {
		t.Fatalf("offline creds = %#v", offline)
	}
	online, err := SelectSandboxCredentials(home, SandboxNetworkIdentityOnline)
	if err != nil {
		t.Fatalf("SelectSandboxCredentials(online) error = %v", err)
	}
	if online.Username != OnlineUsername || online.Password != "online-secret" {
		t.Fatalf("online creds = %#v", online)
	}
}
