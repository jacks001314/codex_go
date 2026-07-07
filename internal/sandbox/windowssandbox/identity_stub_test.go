//go:build !windows

package windowssandbox

import (
	"encoding/base64"
	"testing"
)

func TestSelectSandboxCredentialsReportsDPAPIUnsupportedOffWindows(t *testing.T) {
	home := t.TempDir()
	if err := WriteSetupMarker(home, &SetupMarker{Version: SetupVersion, OfflineUsername: OfflineUsername, OnlineUsername: OnlineUsername}); err != nil {
		t.Fatalf("WriteSetupMarker() error = %v", err)
	}
	if err := WriteSandboxUsersFile(home, &SandboxUsersFile{
		Version: SetupVersion,
		Offline: SandboxUserRecord{
			Username: OfflineUsername,
			Password: base64.StdEncoding.EncodeToString([]byte("ciphertext")),
		},
	}); err != nil {
		t.Fatalf("WriteSandboxUsersFile() error = %v", err)
	}
	if _, err := SelectSandboxCredentials(home, SandboxNetworkIdentityOffline); !IsUnsupported(err) {
		t.Fatalf("SelectSandboxCredentials() error = %v, want unsupported", err)
	}
}
