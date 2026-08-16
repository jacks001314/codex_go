package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"codex_go/rollout"
	"codex_go/state"
)

func writeStartupLegacyRollout(t *testing.T, home, threadID string) string {
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
		string(mustStartupJSON(t, map[string]any{"type": "session_meta", "timestamp": now, "payload": json.RawMessage(data)})),
		string(mustStartupJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "task_started", "turn_id": "turn-1", "started_at": 1700000000}})),
		string(mustStartupJSON(t, map[string]any{"type": "event_msg", "timestamp": now, "payload": map[string]any{"type": "user_message", "client_id": "c1", "message": "hello"}})),
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

func mustStartupJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return data
}

func newStartupTestStateRuntime(t *testing.T) (*state.StateRuntime, *state.SqliteConfig) {
	t.Helper()
	config, err := state.NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	runtime, err := state.InitStateRuntime(context.Background(), config, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime, &config
}

func TestMigrateRolloutsOnStartupMigratesLegacyAndAdvancesCursor(t *testing.T) {
	// Mirrors Rust startup_tests: a legacy rollout on startup triggers full
	// migration and the checked frontier advances; a second startup sees only
	// paginated rollouts and keeps the cursor.
	home := t.TempDir()
	runtime, _ := newStartupTestStateRuntime(t)
	threadID := "123e4567-e89b-42d3-a456-426614174040"
	writeStartupLegacyRollout(t, home, threadID)

	if err := migrateRolloutsOnStartup(context.Background(), home, runtime); err != nil {
		t.Fatalf("migrateRolloutsOnStartup: %v", err)
	}
	meta, err := rollout.FirstSessionMeta(filepath.Join(home, rollout.SessionsSubdir, "rollout-2025-01-01T00-00-00-"+threadID+".jsonl"))
	if err != nil {
		t.Fatalf("FirstSessionMeta: %v", err)
	}
	if meta.HistoryMode != "paginated" {
		t.Fatalf("history mode = %q, want paginated", meta.HistoryMode)
	}
	migrationState, err := state.GetRolloutMigrationState(context.Background(), runtime.StateDB(), legacyToPaginatedMigrationID)
	if err != nil {
		t.Fatalf("GetRolloutMigrationState: %v", err)
	}
	if migrationState == nil || migrationState.LastCheckedThread == nil || migrationState.LastCheckedThread.ThreadID != threadID {
		t.Fatalf("cursor = %#v, want thread %s", migrationState, threadID)
	}

	// Second startup: no legacy rollouts remain, cursor stays.
	if err := migrateRolloutsOnStartup(context.Background(), home, runtime); err != nil {
		t.Fatalf("second migrateRolloutsOnStartup: %v", err)
	}
	again, _ := state.GetRolloutMigrationState(context.Background(), runtime.StateDB(), legacyToPaginatedMigrationID)
	if again == nil || again.LastCheckedThread == nil || again.LastCheckedThread.ThreadID != threadID {
		t.Fatalf("cursor moved on second startup: %#v", again)
	}
}

func TestMigrateRolloutsOnStartupFingerprintsMalformedAndRetriesOnChange(t *testing.T) {
	// Mirrors Rust startup fingerprinting: an empty rollout is recorded as a
	// skip; when it later grows, the skip is invalidated and migration retries.
	home := t.TempDir()
	runtime, _ := newStartupTestStateRuntime(t)
	threadID := "123e4567-e89b-42d3-a456-426614174041"
	emptyPath := filepath.Join(home, rollout.SessionsSubdir, "rollout-2025-01-01T00-00-00-"+threadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(emptyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := migrateRolloutsOnStartup(context.Background(), home, runtime); err != nil {
		t.Fatalf("migrateRolloutsOnStartup: %v", err)
	}
	skipped, err := state.ListRolloutMigrationSkippedRollouts(context.Background(), runtime.StateDB(), legacyToPaginatedMigrationID)
	if err != nil {
		t.Fatalf("ListRolloutMigrationSkippedRollouts: %v", err)
	}
	if len(skipped) != 1 || skipped[0].SkipReason != emptySkipReason {
		t.Fatalf("skipped = %#v, want one empty skip", skipped)
	}

	// The file gains content -> skip invalidated -> migration retries.
	writeStartupLegacyRollout(t, home, threadID)
	if err := migrateRolloutsOnStartup(context.Background(), home, runtime); err != nil {
		t.Fatalf("retry migrateRolloutsOnStartup: %v", err)
	}
	skipped, _ = state.ListRolloutMigrationSkippedRollouts(context.Background(), runtime.StateDB(), legacyToPaginatedMigrationID)
	if len(skipped) != 0 {
		t.Fatalf("skip not cleared after retry: %#v", skipped)
	}
}

func TestThreadCreationCursorParsesRolloutFilename(t *testing.T) {
	cursor := threadCreationCursor(filepath.Join("sessions", "rollout-2025-01-01T00-00-00-123e4567-e89b-42d3-a456-426614174042.jsonl"))
	if cursor == nil || cursor.ThreadID != "123e4567-e89b-42d3-a456-426614174042" {
		t.Fatalf("cursor = %#v", cursor)
	}
	if cursor.ThreadCreatedAt != 1735689600 {
		t.Fatalf("thread_created_at = %d, want 1735689600 (2025-01-01T00:00:00Z)", cursor.ThreadCreatedAt)
	}
	if got := threadCreationCursor(filepath.Join("sessions", "bogus.jsonl")); got != nil {
		t.Fatalf("bogus cursor = %#v, want nil", got)
	}
}
