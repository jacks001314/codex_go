//go:build windows

package win

import (
	"os"
	"strings"
	"testing"

	"codex_go/sandbox/windowssandbox"
	"golang.org/x/sys/windows"
)

func TestOpenSetupLogProvisioningModeCreatesFreshLog(t *testing.T) {
	dir := t.TempDir()
	first, err := openSetupLog(dir, setupModeProvision)
	if err != nil {
		t.Fatalf("openSetupLog(provision) error = %v", err)
	}
	first.Close()
	second, err := openSetupLog(dir, setupModeProvision)
	if err != nil {
		t.Fatalf("openSetupLog(provision, second) error = %v", err)
	}
	second.Close()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("provisioning logs = %v, %v; want 2 fresh logs", entries, err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "sandbox-") || !strings.HasSuffix(entry.Name(), ".log") {
			t.Fatalf("provisioning log name = %q", entry.Name())
		}
	}
}

func TestOpenSetupLogLegacyModeUsesDailyLog(t *testing.T) {
	dir := t.TempDir()
	log, err := openSetupLog(dir, setupModeFull)
	if err != nil {
		t.Fatalf("openSetupLog(full) error = %v", err)
	}
	log.Close()
	want := windowssandbox.CurrentLogFilePathForBaseDir(dir)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("legacy log %s missing: %v", want, err)
	}
}

func TestPrepareSetupMarkerProvisioningModeRetainsHandleUntilCommit(t *testing.T) {
	home := t.TempDir()
	payload := &setupPayload{CodexHome: home, Mode: setupModeProvision, RealUser: currentAccountName(t)}
	marker, err := prepareSetupMarker(payload)
	if err != nil {
		t.Fatalf("prepareSetupMarker(provision) error = %v", err)
	}
	if marker == nil {
		t.Fatal("provisioning setup must retain the marker handle")
	}
	markerPath := windowssandbox.SetupMarkerPath(home)
	if _, err := os.OpenFile(markerPath, os.O_WRONLY, 0o600); err == nil {
		marker.Close()
		t.Fatal("retained marker must stay exclusively held")
	}
	if err := commitSetupMarker(marker, home, windowssandbox.OfflineUsername, windowssandbox.OnlineUsername, []uint16{8080}, true); err != nil {
		marker.Close()
		t.Fatalf("commitSetupMarker(provision) error = %v", err)
	}
	marker.Close()
	got, err := windowssandbox.ReadSetupMarker(home)
	if err != nil || got == nil {
		t.Fatalf("ReadSetupMarker() = %#v, %v", got, err)
	}
	if got.OfflineUsername != windowssandbox.OfflineUsername || len(got.ProxyPorts) != 1 || got.ProxyPorts[0] != 8080 || !got.AllowLocalBinding {
		t.Fatalf("marker = %#v", got)
	}
}

func TestPrepareSetupMarkerLegacyModeReopensAtCommit(t *testing.T) {
	home := t.TempDir()
	payload := &setupPayload{CodexHome: home, Mode: setupModeFull}
	marker, err := prepareSetupMarker(payload)
	if err != nil {
		t.Fatalf("prepareSetupMarker(full) error = %v", err)
	}
	if marker != nil {
		marker.Close()
		t.Fatal("legacy setup must reopen the marker rather than retain it")
	}
	if err := commitSetupMarker(nil, home, windowssandbox.OfflineUsername, windowssandbox.OnlineUsername, nil, false); err != nil {
		t.Fatalf("commitSetupMarker(full) error = %v", err)
	}
	if _, err := os.Stat(windowssandbox.SetupMarkerPath(home)); err != nil {
		t.Fatalf("legacy marker missing: %v", err)
	}
}

func currentAccountName(t *testing.T) string {
	t.Helper()
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	account, _, _, err := user.User.Sid.LookupAccount("")
	if err != nil {
		t.Fatalf("LookupAccount() error = %v", err)
	}
	return account
}
