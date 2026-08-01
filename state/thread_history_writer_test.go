package state

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

func TestPaginatedSessionWriterProjectsCanonicalItemLifecycle(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	now := time.Date(2026, 7, 31, 3, 4, 5, 0, time.UTC)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, SessionID: "thread-writer", ThreadID: "thread-writer",
		HistoryMode: "paginated", ModelProvider: "openai", CWD: home, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatal(err)
	}
	first := session.Item{
		ID: "command-start", Type: "commandExecution", CallID: "call-1", CreatedAt: now.Add(time.Second),
		Data:     map[string]any{"command": []string{"echo", "ok"}, "cwd": home, "status": "inProgress"},
		Metadata: map[string]any{"turnId": "turn-1", "startedAtMs": now.Add(time.Second).UnixMilli()},
	}
	completed := session.Item{
		ID: "command-end", Type: "commandExecution", CallID: "call-1", Text: "ok\n", CreatedAt: now.Add(2 * time.Second),
		Data:     map[string]any{"command": []string{"echo", "ok"}, "cwd": home, "status": "completed", "exitCode": int64(0)},
		Metadata: map[string]any{"turnId": "turn-1", "startedAtMs": now.Add(time.Second).UnixMilli(), "completedAtMs": now.Add(2 * time.Second).UnixMilli()},
	}
	if err := rollout.AppendSessionItems(recorder, []session.Item{first, completed}, now); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnComplete("turn-1", now.Add(3*time.Second), 3_000); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := MaterializeThreadHistory(ctx, db, "thread-writer", path, 0, nil); err != nil {
		t.Fatal(err)
	}
	var rolloutOrdinal, updatedOrdinal, createdAtMS int64
	var itemType, itemJSON string
	if err := db.QueryRowContext(ctx, `SELECT rollout_ordinal, updated_at_ordinal, created_at_ms, item_type, item_json
FROM thread_items WHERE thread_id = ? AND turn_id = ? AND item_id = ?`, "thread-writer", "turn-1", "call-1").Scan(&rolloutOrdinal, &updatedOrdinal, &createdAtMS, &itemType, &itemJSON); err != nil {
		t.Fatal(err)
	}
	if rolloutOrdinal != 3 || updatedOrdinal != 3 || createdAtMS != now.Add(time.Second).UnixMilli() || itemType != "commandExecution" {
		t.Fatalf("projected lifecycle = ordinal %d updated %d created %d type %q", rolloutOrdinal, updatedOrdinal, createdAtMS, itemType)
	}
	rolloutData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if startedEvents, completedEvents := strings.Count(string(rolloutData), `"type":"item_started"`), strings.Count(string(rolloutData), `"type":"item_completed"`); startedEvents != 1 || completedEvents != 1 {
		t.Fatalf("item lifecycle events = started %d completed %d", startedEvents, completedEvents)
	}
	var public map[string]any
	if err := json.Unmarshal([]byte(itemJSON), &public); err != nil {
		t.Fatal(err)
	}
	if public["type"] != "commandExecution" || public["id"] != "call-1" || public["status"] != "completed" || public["aggregatedOutput"] != "ok\n" || public["exitCode"] != float64(0) {
		t.Fatalf("projected public item = %#v", public)
	}
}

func mustSQLiteConfig(t *testing.T, home string) SqliteConfig {
	t.Helper()
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	return config
}
