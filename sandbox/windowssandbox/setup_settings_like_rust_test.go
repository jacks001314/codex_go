package windowssandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeSetupFixtures(t *testing.T, home string, marker SetupMarker) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(SetupMarkerPath(home)), 0o700); err != nil {
		t.Fatalf("create sandbox dir: %v", err)
	}
	markerData, err := json.Marshal(marker)
	if err != nil {
		t.Fatalf("marshal marker: %v", err)
	}
	if err := os.WriteFile(SetupMarkerPath(home), markerData, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	usersData, err := json.Marshal(SandboxUsersFile{Version: SetupVersion})
	if err != nil {
		t.Fatalf("marshal users: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(SandboxUsersPath(home)), 0o700); err != nil {
		t.Fatalf("create sandbox secrets dir: %v", err)
	}
	if err := os.WriteFile(SandboxUsersPath(home), usersData, 0o600); err != nil {
		t.Fatalf("write users: %v", err)
	}
}

func TestSandboxSetupIsCompleteWithSettingsComparesSortedProxySettings(t *testing.T) {
	home := t.TempDir()
	writeSetupFixtures(t, home, SetupMarker{
		Version:    SetupVersion,
		ProxyPorts: []uint16{8080, 9090},
	})
	cases := []struct {
		name     string
		settings OfflineProxySettings
		want     bool
	}{
		{"same order", OfflineProxySettings{ProxyPorts: []uint16{8080, 9090}}, true},
		{"different order", OfflineProxySettings{ProxyPorts: []uint16{9090, 8080}}, true},
		{"subset", OfflineProxySettings{ProxyPorts: []uint16{8080}}, false},
		{"extra port", OfflineProxySettings{ProxyPorts: []uint16{8080, 9090, 7070}}, false},
		{"local binding mismatch", OfflineProxySettings{ProxyPorts: []uint16{8080, 9090}, AllowLocalBinding: true}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := SandboxSetupIsCompleteWithSettings(home, testCase.settings); got != testCase.want {
				t.Fatalf("SandboxSetupIsCompleteWithSettings(%+v) = %v, want %v", testCase.settings, got, testCase.want)
			}
		})
	}
}

func TestSandboxSetupIsCompleteWithSettingsRequiresBothFiles(t *testing.T) {
	home := t.TempDir()
	writeSetupFixtures(t, home, SetupMarker{Version: SetupVersion})
	whileUsers := OfflineProxySettings{}
	if !SandboxSetupIsCompleteWithSettings(home, whileUsers) {
		t.Fatal("matching marker and users must report complete setup")
	}
	if err := os.Remove(SandboxUsersPath(home)); err != nil {
		t.Fatalf("remove users file: %v", err)
	}
	if SandboxSetupIsCompleteWithSettings(home, whileUsers) {
		t.Fatal("a missing credentials file must not report complete setup")
	}
}

func TestSandboxSetupIsCompleteWithSettingsRefusesAStaleVersion(t *testing.T) {
	home := t.TempDir()
	writeSetupFixtures(t, home, SetupMarker{Version: SetupVersion + 1})
	if SandboxSetupIsCompleteWithSettings(home, OfflineProxySettings{}) {
		t.Fatal("a stale marker version must not report complete setup")
	}
}
