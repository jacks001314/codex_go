package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectExternalSessionMigration(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "projects", "repo", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"user","cwd":"/repo","message":{"content":"hi"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(t.TempDir())
	service.SetExternalAgentHome(home)
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true})
	var sessions []SessionMigration
	for _, item := range detected.Items {
		if item.ItemType == MigrationSessions && item.Details != nil {
			sessions = item.Details.Sessions
		}
	}
	if len(sessions) != 1 || sessions[0].Path != path || sessions[0].CWD != "/repo" {
		t.Fatalf("sessions = %#v", sessions)
	}
}
