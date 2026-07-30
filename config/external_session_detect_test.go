package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectExternalSessionMigration(t *testing.T) {
	home := t.TempDir()
	codexHome := t.TempDir()
	cwd := t.TempDir()
	path := filepath.Join(home, "projects", "repo", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","cwd":` + jsonQuoted(cwd) + `,"timestamp":"2026-07-28T10:00:00Z","message":{"content":"hi"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(codexHome)
	service.SetExternalAgentHome(home)
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true})
	var sessions []SessionMigration
	for _, item := range detected.Items {
		if item.ItemType == MigrationSessions && item.Details != nil {
			sessions = item.Details.Sessions
		}
	}
	if len(sessions) != 1 || sessions[0].Path != path || sessions[0].CWD != cwd || sessions[0].Title == nil || *sessions[0].Title != "hi" {
		t.Fatalf("sessions = %#v", sessions)
	}
	if err := RecordExternalSessionImport(codexHome, path, "thread-1"); err != nil {
		t.Fatal(err)
	}
	if detectedAgain := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true}); len(detectedAgain.Items) != 0 {
		t.Fatalf("imported session detected again = %#v", detectedAgain.Items)
	}
	changedBody := `{"type":"user","cwd":` + jsonQuoted(cwd) + `,"timestamp_ms":1800000000000,"message":{"content":"changed"}}`
	if err := os.WriteFile(path, []byte(changedBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true}); len(changed.Items) != 1 || changed.Items[0].ItemType != MigrationSessions {
		t.Fatalf("changed imported session not redetected = %#v", changed.Items)
	}
}

func TestExternalSessionImportLedgerPersistsTitle(t *testing.T) {
	codexHome := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(sourcePath, []byte("session"), 0o600); err != nil {
		t.Fatal(err)
	}
	title := "Original session title"
	if err := RecordExternalSessionImports(codexHome, []ExternalSessionImportCompletion{{
		SourcePath: sourcePath, ImportedThreadID: "thread-1", Title: &title,
	}}); err != nil {
		t.Fatal(err)
	}
	ledger, err := loadExternalSessionImportLedger(codexHome)
	if err != nil || len(ledger.Records) != 1 || ledger.Records[0].Title == nil || *ledger.Records[0].Title != title {
		t.Fatalf("ledger = %#v, %v", ledger, err)
	}
}
