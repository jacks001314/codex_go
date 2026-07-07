package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxDirsMatchRustLayout(t *testing.T) {
	home := filepath.Join("C:", "Users", "example", ".codex")
	if SandboxDir(home) != filepath.Join(home, ".sandbox") {
		t.Fatalf("SandboxDir() = %q", SandboxDir(home))
	}
	if SandboxBinDir(home) != filepath.Join(home, ".sandbox-bin") {
		t.Fatalf("SandboxBinDir() = %q", SandboxBinDir(home))
	}
	if SandboxSecretsDir(home) != filepath.Join(home, ".sandbox-secrets") {
		t.Fatalf("SandboxSecretsDir() = %q", SandboxSecretsDir(home))
	}
	if SetupMarkerPath(home) != filepath.Join(home, ".sandbox", "setup_marker.json") {
		t.Fatalf("SetupMarkerPath() = %q", SetupMarkerPath(home))
	}
	if SandboxUsersPath(home) != filepath.Join(home, ".sandbox-secrets", "sandbox_users.json") {
		t.Fatalf("SandboxUsersPath() = %q", SandboxUsersPath(home))
	}
}

func TestSetupMarkerRoundTrip(t *testing.T) {
	home := t.TempDir()
	created := "2026-07-04T00:00:00Z"
	marker := &SetupMarker{
		Version:           SetupVersion,
		OfflineUsername:   OfflineUsername,
		OnlineUsername:    OnlineUsername,
		CreatedAt:         &created,
		ProxyPorts:        []uint16{8080},
		AllowLocalBinding: true,
	}
	if err := WriteSetupMarker(home, marker); err != nil {
		t.Fatalf("WriteSetupMarker() error = %v", err)
	}
	got, err := ReadSetupMarker(home)
	if err != nil {
		t.Fatalf("ReadSetupMarker() error = %v", err)
	}
	if !got.VersionMatches() || got.OfflineUsername != OfflineUsername || len(got.ProxyPorts) != 1 {
		t.Fatalf("marker = %#v", got)
	}
	if _, err := os.Stat(SetupMarkerPath(home)); err != nil {
		t.Fatalf("marker file missing: %v", err)
	}
}
