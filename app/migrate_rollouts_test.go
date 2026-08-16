package app

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"codex_go/rollout"
)

func writeLegacyRolloutForProjection(t *testing.T, home string, threadID string) string {
	t.Helper()
	path := filepath.Join(home, rollout.SessionsSubdir, "rollout-2025-01-01T00-00-00-"+threadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	now := "2025-01-01T00:00:00Z"
	meta := map[string]any{
		"id": threadID, "session_id": "s", "timestamp": now, "cwd": "/w",
		"originator": "o", "model": "m", "cli_version": "v",
	}
	data, _ := json.Marshal(meta)
	lines := []string{
		string(mustAppJSON(t, map[string]any{"type": "session_meta", "timestamp": now, "payload": json.RawMessage(data)})),
		string(mustAppJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "task_started", "turn_id": "turn-1", "started_at": 1700000000}})),
		string(mustAppJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "user_message", "client_id": "c1", "message": "hello"}})),
		string(mustAppJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "agent_message", "message": "hi"}})),
		"",
	}
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func mustAppJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return data
}

func TestProjectMigratedRolloutsWritesThreadHistorySQLite(t *testing.T) {
	// Mirrors Rust migrate_one_rollout's SQLite projection step: after
	// publishing a paginated rollout, the durable thread-history projection is
	// materialized so app-server reads come from SQLite.
	home := t.TempDir()
	threadID := "123e4567-e89b-42d3-a456-426614174030"
	path := writeLegacyRolloutForProjection(t, home, threadID)
	if err := rollout.CanonicalizeRollout(home, path); err != nil {
		t.Fatalf("CanonicalizeRollout: %v", err)
	}
	report := &rollout.MigrationReport{Outcomes: []rollout.MigrationOutcome{
		{ThreadID: &threadID, RolloutPath: path, Status: rollout.MigrationStatusMigrated},
	}}
	if err := projectMigratedRollouts(home, report); err != nil {
		t.Fatalf("projectMigratedRollouts: %v", err)
	}
	db, err := openRouterSQLiteForProjection(t, filepath.Join(home, "thread_history_1.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	var turns, items int
	var nextOffset, nextOrdinal int64
	if err := db.QueryRow("SELECT COUNT(*) FROM thread_turns").Scan(&turns); err != nil {
		t.Fatalf("count turns: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM thread_items").Scan(&items); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if err := db.QueryRow("SELECT next_rollout_byte_offset, next_rollout_ordinal FROM thread_history_projection_state").Scan(&nextOffset, &nextOrdinal); err != nil {
		t.Fatalf("read projection state: %v", err)
	}
	if turns != 1 || items != 2 {
		t.Fatalf("projection turns=%d items=%d, want 1/2", turns, items)
	}
	if nextOrdinal != 4 {
		t.Fatalf("projection next_ordinal=%d, want 4 (meta+started+2 items)", nextOrdinal)
	}
	if nextOffset == 0 {
		t.Fatal("projection byte offset is zero; nothing was projected")
	}
}

func openRouterSQLiteForProjection(t *testing.T, path string) (*sql.DB, error) {
	t.Helper()
	return sql.Open("sqlite", path)
}
