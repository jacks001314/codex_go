package state

import (
	"database/sql"
	"testing"
)

// Mirrors Rust #47114: the projection stores the producer-recorded item start
// and completion times, preserves existing non-null values on later updates, and
// leaves unknown completion times null.
func TestThreadHistoryPersistsItemLifecycleTimestampsLikeRust(t *testing.T) {
	db, path := openProjectionFixture(t, historyMetaLine(0)+
		historyTurnLine(1, "turn-1", "2025-01-01T00:00:00Z")+
		`{"timestamp":"2025-01-01T00:00:02Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","started_at_ms":1000,"completed_at_ms":2000,"item":{"type":"AgentMessage","id":"agent-1","content":[],"phase":"final_answer"}}}`+"\n")
	assertMaterialized(t, db, path)

	read := func(itemID string) (sql.NullInt64, sql.NullInt64) {
		t.Helper()
		var started, completed sql.NullInt64
		if err := db.QueryRow(`SELECT started_at_ms, completed_at_ms FROM thread_items WHERE thread_id = ? AND item_id = ?`, "thread-1", itemID).Scan(&started, &completed); err != nil {
			t.Fatal(err)
		}
		return started, completed
	}
	started, completed := read("agent-1")
	if !started.Valid || started.Int64 != 1000 || !completed.Valid || completed.Int64 != 2000 {
		t.Fatalf("stored lifecycle = started:%v completed:%v, want 1000/2000", started, completed)
	}

	// A duplicate completion that omits the times must not erase the recorded
	// values; the projection only fills unknown columns.
	appendProjectionFixture(t, path,
		`{"timestamp":"2025-01-01T00:00:03Z","ordinal":3,"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"AgentMessage","id":"agent-1","content":[],"phase":"final_answer"}}}`+"\n")
	assertMaterialized(t, db, path)
	started, completed = read("agent-1")
	if !started.Valid || started.Int64 != 1000 || !completed.Valid || completed.Int64 != 2000 {
		t.Fatalf("lifecycle after duplicate = started:%v completed:%v, want the recorded 1000/2000", started, completed)
	}

	appendProjectionFixture(t, path,
		`{"timestamp":"2025-01-01T00:00:05Z","ordinal":4,"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","started_at_ms":5000,"item":{"type":"AgentMessage","id":"agent-2","content":[],"phase":"final_answer"}}}`+"\n")
	assertMaterialized(t, db, path)
	started, completed = read("agent-2")
	if !started.Valid || started.Int64 != 5000 || completed.Valid {
		t.Fatalf("unknown completion = started:%v completed:%v, want 5000/null", started, completed)
	}
}
