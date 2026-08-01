package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeThreadHistorySkipsInferredMalformedOrdinalGapLikeRust(t *testing.T) {
	db, path := openProjectionFixture(t, historyMetaLine(0))
	assertMaterialized(t, db, path)
	before := requireProjectionState(t, db)
	appendProjectionFixture(t, path, "{not json}\n"+historyTurnLine(2, "turn-2", "2025-01-01T00:00:00Z"))
	assertMaterialized(t, db, path)
	state := requireProjectionState(t, db)
	info, _ := os.Stat(path)
	if state.NextRolloutByteOffset != uint64(info.Size()) || state.NextRolloutOrdinal != 3 {
		t.Fatalf("state = %+v, file size = %d", state, info.Size())
	}
	if state.NextRolloutByteOffset <= before.NextRolloutByteOffset {
		t.Fatalf("checkpoint did not advance: before=%+v after=%+v", before, state)
	}
	var ordinal int64
	if err := db.QueryRow(`SELECT rollout_ordinal FROM thread_turns WHERE thread_id = ? AND turn_id = ?`, "thread-1", "turn-2").Scan(&ordinal); err != nil || ordinal != 2 {
		t.Fatalf("turn ordinal/error = %d/%v", ordinal, err)
	}
}

func TestMaterializeThreadHistoryWaitsForUnprojectableLinesLikeRust(t *testing.T) {
	db, path := openProjectionFixture(t, historyMetaLine(0))
	assertMaterialized(t, db, path)
	before := requireProjectionState(t, db)
	appendProjectionFixture(t, path, `{"timestamp":"2025-01-01T00:00:00Z","ordinal":1,"type":"future_item","payload":{}}`+"\n")
	assertMaterialized(t, db, path)
	if got := requireProjectionState(t, db); got != before {
		t.Fatalf("unprojectable tail advanced state: before=%+v after=%+v", before, got)
	}
	appendProjectionFixture(t, path,
		historyTurnLine(1, "retry-turn", "2025-01-01T00:00:00Z")+
			`{"timestamp":"2025-01-01T00:00:00Z","ordinal":2,"type":"future_item","payload":{}}`+"\n"+
			historyTurnLine(3, "turn-3", "2025-01-01T00:00:00Z"))
	assertMaterialized(t, db, path)
	state := requireProjectionState(t, db)
	if state.NextRolloutOrdinal != 4 {
		t.Fatalf("state = %+v", state)
	}
	rows, err := db.Query(`SELECT turn_id, rollout_ordinal FROM thread_turns WHERE thread_id = ? ORDER BY rollout_ordinal`, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var turnID string
		var ordinal int64
		if err := rows.Scan(&turnID, &ordinal); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s:%d", turnID, ordinal))
	}
	if strings.Join(got, ",") != "retry-turn:1,turn-3:3" {
		t.Fatalf("turns = %v", got)
	}
}

func TestMaterializeThreadHistoryUsesEventTimestampBeforeRolloutTimestampLikeRust(t *testing.T) {
	contents := historyMetaLine(0) +
		historyTurnLine(1, "turn-1", "not-a-timestamp") +
		`{"timestamp":"not-a-timestamp","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","started_at_ms":0,"completed_at_ms":1,"item":{"type":"AgentMessage","id":"agent-1","content":[],"phase":"final_answer"}}}` + "\n"
	db, path := openProjectionFixture(t, contents)
	assertMaterialized(t, db, path)
	var createdAt, ordinal int64
	var itemType string
	if err := db.QueryRow(`SELECT created_at_ms, rollout_ordinal, item_type FROM thread_items WHERE thread_id = ? AND item_id = ?`, "thread-1", "agent-1").Scan(&createdAt, &ordinal, &itemType); err != nil {
		t.Fatal(err)
	}
	if createdAt != 0 || ordinal != 2 || itemType != "agentMessage" {
		t.Fatalf("item = created:%d ordinal:%d type:%s", createdAt, ordinal, itemType)
	}
	if got := requireProjectionState(t, db); got.NextRolloutOrdinal != 3 {
		t.Fatalf("state = %+v", got)
	}
}

func TestMaterializeThreadHistoryDefersInvalidFallbackTimestampLikeRust(t *testing.T) {
	db, path := openProjectionFixture(t, historyMetaLine(0))
	assertMaterialized(t, db, path)
	before := requireProjectionState(t, db)
	appendProjectionFixture(t, path, `{"timestamp":"not-a-timestamp","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"UserMessage","id":"user-bad","content":[]}}}`+"\n")
	assertMaterialized(t, db, path)
	if got := requireProjectionState(t, db); got != before {
		t.Fatalf("invalid fallback timestamp advanced state: before=%+v after=%+v", before, got)
	}
	appendProjectionFixture(t, path, `{"timestamp":"also-invalid","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","started_at_ms":7,"item":{"type":"UserMessage","id":"user-good","content":[]}}}`+"\n")
	assertMaterialized(t, db, path)
	var createdAt int64
	if err := db.QueryRow(`SELECT created_at_ms FROM thread_items WHERE thread_id = ? AND item_id = ?`, "thread-1", "user-good").Scan(&createdAt); err != nil || createdAt != 7 {
		t.Fatalf("created/error = %d/%v", createdAt, err)
	}
}

func TestMaterializeThreadHistoryRollsBackRowsAndCheckpointTogetherLikeRust(t *testing.T) {
	db, path := openProjectionFixture(t, historyMetaLine(0))
	assertMaterialized(t, db, path)
	before := requireProjectionState(t, db)
	if _, err := db.Exec(`INSERT INTO thread_turns (thread_id, turn_id, rollout_ordinal, status) VALUES (?, ?, ?, ?)`, "thread-1", "conflict", 1, "inProgress"); err != nil {
		t.Fatal(err)
	}
	appendProjectionFixture(t, path, historyTurnLine(1, "turn-1", "2025-01-01T00:00:00Z"))
	err := MaterializeThreadHistory(context.Background(), db, "thread-1", path, 0, nil)
	if err == nil {
		t.Fatal("expected SQLite projection failure")
	}
	if got := requireProjectionState(t, db); got != before {
		t.Fatalf("failed projection advanced checkpoint: before=%+v after=%+v", before, got)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM thread_turns WHERE thread_id = ? AND turn_id = ?`, "thread-1", "turn-1").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed projection row count/error = %d/%v", count, err)
	}
}

func openProjectionFixture(t *testing.T, contents string) (*sql.DB, string) {
	t.Helper()
	home := t.TempDir()
	db, err := OpenThreadHistoryDB(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	path := filepath.Join(home, "rollout.jsonl")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return db, path
}

func assertMaterialized(t *testing.T, db *sql.DB, path string) {
	t.Helper()
	if err := MaterializeThreadHistory(context.Background(), db, "thread-1", path, 0, nil); err != nil {
		t.Fatal(err)
	}
}

func requireProjectionState(t *testing.T, db *sql.DB) ThreadHistoryProjectionState {
	t.Helper()
	state, err := LoadThreadHistoryProjectionState(context.Background(), db, "thread-1")
	if err != nil || state == nil {
		t.Fatalf("projection state/error = %#v/%v", state, err)
	}
	return *state
}

func appendProjectionFixture(t *testing.T, path string, contents string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(contents); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func historyMetaLine(ordinal uint64) string {
	return fmt.Sprintf(`{"timestamp":"2025-01-01T00:00:00Z","ordinal":%d,"type":"session_meta","payload":{"id":"thread-1","timestamp":"2025-01-01T00:00:00Z","history_mode":"paginated"}}`, ordinal) + "\n"
}

func historyTurnLine(ordinal uint64, turnID string, timestamp string) string {
	return fmt.Sprintf(`{"timestamp":%q,"ordinal":%d,"type":"event_msg","payload":{"type":"turn_started","turn_id":%q,"started_at":10}}`, timestamp, ordinal, turnID) + "\n"
}
