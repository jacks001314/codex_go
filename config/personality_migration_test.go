package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/rollout"
)

func TestMaybeMigratePersonalityNoSessionsWritesMarkerOnlyLikeRust(t *testing.T) {
	home := t.TempDir()

	status, err := MaybeMigratePersonality(home)
	if err != nil {
		t.Fatalf("MaybeMigratePersonality() error = %v", err)
	}
	if status != PersonalityMigrationSkippedNoSessions {
		t.Fatalf("status = %s", status)
	}
	if _, err := os.Stat(filepath.Join(home, PersonalityMigrationFilename)); err != nil {
		t.Fatalf("marker stat error = %v", err)
	}
	if _, err := os.Stat(ConfigPath(home)); !os.IsNotExist(err) {
		t.Fatalf("config.toml should not be created, stat err=%v", err)
	}
}

func TestMaybeMigratePersonalityUserSessionSetsPragmaticLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte("model = \"gpt-5.4\"\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	writePersonalityMigrationRollout(t, home, false, true)

	status, err := MaybeMigratePersonality(home)
	if err != nil {
		t.Fatalf("MaybeMigratePersonality() error = %v", err)
	}
	if status != PersonalityMigrationApplied {
		t.Fatalf("status = %s", status)
	}
	body, err := os.ReadFile(ConfigPath(home))
	if err != nil {
		t.Fatalf("read config error = %v", err)
	}
	text := string(body)
	if !strings.Contains(text, `model = "gpt-5.4"`) || !strings.Contains(text, `personality = "pragmatic"`) {
		t.Fatalf("config.toml = %s", text)
	}
	if _, err := os.Stat(filepath.Join(home, PersonalityMigrationFilename)); err != nil {
		t.Fatalf("marker stat error = %v", err)
	}
}

func TestMaybeMigratePersonalityMetaOnlyRolloutSkipsLikeRust(t *testing.T) {
	home := t.TempDir()
	writePersonalityMigrationRollout(t, home, false, false)

	status, err := MaybeMigratePersonality(home)
	if err != nil {
		t.Fatalf("MaybeMigratePersonality() error = %v", err)
	}
	if status != PersonalityMigrationSkippedNoSessions {
		t.Fatalf("status = %s", status)
	}
	if _, err := os.Stat(ConfigPath(home)); !os.IsNotExist(err) {
		t.Fatalf("config.toml should not be created, stat err=%v", err)
	}
}

func TestMaybeMigratePersonalityExplicitGlobalPersonalitySkipsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte("personality = \"friendly\"\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	writePersonalityMigrationRollout(t, home, false, true)

	status, err := MaybeMigratePersonality(home)
	if err != nil {
		t.Fatalf("MaybeMigratePersonality() error = %v", err)
	}
	if status != PersonalityMigrationSkippedExplicitPersonality {
		t.Fatalf("status = %s", status)
	}
	body, err := os.ReadFile(ConfigPath(home))
	if err != nil {
		t.Fatalf("read config error = %v", err)
	}
	if string(body) != "personality = \"friendly\"\n" {
		t.Fatalf("config.toml = %s", body)
	}
}

func TestMaybeMigratePersonalityArchivedSessionSetsPragmaticLikeRust(t *testing.T) {
	home := t.TempDir()
	writePersonalityMigrationRollout(t, home, true, true)

	status, err := MaybeMigratePersonality(home)
	if err != nil {
		t.Fatalf("MaybeMigratePersonality() error = %v", err)
	}
	if status != PersonalityMigrationApplied {
		t.Fatalf("status = %s", status)
	}
	body, err := os.ReadFile(ConfigPath(home))
	if err != nil {
		t.Fatalf("read config error = %v", err)
	}
	if !strings.Contains(string(body), `personality = "pragmatic"`) {
		t.Fatalf("config.toml = %s", body)
	}
}

func writePersonalityMigrationRollout(t *testing.T, home string, archived bool, includeUser bool) {
	t.Helper()
	root := filepath.Join(home, rollout.SessionsSubdir, "2025", "01", "01")
	if archived {
		root = filepath.Join(home, rollout.ArchivedSessionsSubdir)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll rollout root error = %v", err)
	}
	path := filepath.Join(root, "rollout-2025-01-01T00-00-00-thread-personality.jsonl")
	lines := []string{
		`{"timestamp":"2025-01-01T00:00:00Z","type":"session_meta","payload":{"id":"thread-personality","timestamp":"2025-01-01T00:00:00Z","source":"cli","model_provider":"openai","cwd":"."}}`,
	}
	if includeUser {
		lines = append(lines, `{"timestamp":"2025-01-01T00:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}`)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout error = %v", err)
	}
}
