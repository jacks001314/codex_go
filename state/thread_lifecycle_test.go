package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/rollout"
)

func TestThreadArchiveStateAndStrictDeleteMatchRustOrdering(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime := newBackfillTestRuntimeAt(t, home)
	path := writeBackfillRollout(t, home, "lifecycle-thread", time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC), false)
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `UPDATE threads SET recency_at = 123, recency_at_ms = 123456 WHERE id = ?`, "lifecycle-thread"); err != nil {
		t.Fatal(err)
	}

	archivedPath, err := rollout.Archive(path, home)
	if err != nil {
		t.Fatal(err)
	}
	archiveTime := time.Date(2026, 7, 31, 4, 5, 6, 0, time.UTC)
	if err := runtime.MarkThreadArchived(ctx, "lifecycle-thread", archivedPath, archiveTime); err != nil {
		t.Fatal(err)
	}
	assertThreadArchiveRow(t, runtime, "lifecycle-thread", archivedPath, true, sql.NullInt64{Int64: archiveTime.Unix(), Valid: true})

	restoredPath, err := rollout.Unarchive(archivedPath, home)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.MarkThreadUnarchived(ctx, "lifecycle-thread", restoredPath); err != nil {
		t.Fatal(err)
	}
	assertThreadArchiveRow(t, runtime, "lifecycle-thread", restoredPath, false, sql.NullInt64{})
	if filepath.Dir(restoredPath) != filepath.Join(home, rollout.SessionsSubdir, "2026", "07", "31") {
		t.Fatalf("restored path = %q", restoredPath)
	}

	if _, err := runtime.StateDB().ExecContext(ctx, `INSERT INTO thread_dynamic_tools (thread_id, position, name, description, input_schema) VALUES (?, 0, 'tool', 'desc', '{}')`, "lifecycle-thread"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `INSERT INTO thread_spawn_edges (parent_thread_id, child_thread_id, status) VALUES (?, 'child-thread', 'running')`, "lifecycle-thread"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.LogsDB().ExecContext(ctx, `INSERT INTO logs (ts, ts_nanos, level, target, feedback_log_body, thread_id) VALUES (1, 0, 'INFO', 'test', 'body', ?)`, "lifecycle-thread"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.GoalsDB().ExecContext(ctx, `INSERT INTO thread_goals (thread_id, goal_id, objective, status, created_at_ms, updated_at_ms) VALUES (?, 'goal', 'objective', 'active', 1, 1)`, "lifecycle-thread"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.GoalsDB().ExecContext(ctx, `INSERT INTO thread_goal_continuation_deferrals (thread_id) VALUES (?)`, "lifecycle-thread"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.MemoriesDB().ExecContext(ctx, `INSERT INTO stage1_outputs (thread_id, source_updated_at, raw_memory, rollout_summary, generated_at, selected_for_phase2) VALUES (?, 1, 'raw', 'summary', 1, 1)`, "lifecycle-thread"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.MemoriesDB().ExecContext(ctx, `INSERT INTO jobs (kind, job_key, status, retry_remaining) VALUES ('memory_stage1', ?, 'pending', 3)`, "lifecycle-thread"); err != nil {
		t.Fatal(err)
	}
	historyDB, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := historyDB.ExecContext(ctx, `INSERT INTO thread_history_projection_state (thread_id, next_rollout_byte_offset, next_rollout_ordinal) VALUES (?, 1, 1)`, "lifecycle-thread"); err != nil {
		t.Fatal(err)
	}

	if err := runtime.DeleteThreadHistory(ctx, "lifecycle-thread"); err != nil {
		t.Fatal(err)
	}
	deleted, err := runtime.DeleteThreadsStrict(ctx, []string{"lifecycle-thread", "lifecycle-thread", ""})
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteThreadsStrict() deleted=%d error=%v", deleted, err)
	}
	assertTableThreadCount(t, runtime.StateDB(), "threads", "id", "lifecycle-thread", 0)
	assertTableThreadCount(t, runtime.StateDB(), "thread_dynamic_tools", "thread_id", "lifecycle-thread", 0)
	assertTableThreadCount(t, runtime.StateDB(), "thread_spawn_edges", "parent_thread_id", "lifecycle-thread", 0)
	assertTableThreadCount(t, runtime.LogsDB(), "logs", "thread_id", "lifecycle-thread", 0)
	assertTableThreadCount(t, runtime.GoalsDB(), "thread_goals", "thread_id", "lifecycle-thread", 0)
	assertTableThreadCount(t, runtime.GoalsDB(), "thread_goal_continuation_deferrals", "thread_id", "lifecycle-thread", 0)
	assertTableThreadCount(t, runtime.MemoriesDB(), "stage1_outputs", "thread_id", "lifecycle-thread", 0)
	assertTableThreadCount(t, historyDB, "thread_history_projection_state", "thread_id", "lifecycle-thread", 0)
	var status string
	if err := runtime.MemoriesDB().QueryRowContext(ctx, `SELECT status FROM jobs WHERE kind = 'memory_consolidate_global' AND job_key = 'global'`).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("consolidation job status = %q, %v", status, err)
	}
}

func TestReadRepairCompressedRolloutRebuildsCanonicalStatePath(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime := newBackfillTestRuntimeAt(t, home)
	plain := writeBackfillRollout(t, home, "compressed-repair", time.Date(2026, 7, 31, 2, 3, 4, 0, time.UTC), false)
	compressed := compressBackfillRollout(t, plain)
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReadRepairRolloutPath(ctx, "compressed-repair", compressed, false); err != nil {
		t.Fatal(err)
	}
	path, found, err := runtime.FindRolloutPathByID(ctx, "compressed-repair", nil)
	if err != nil || !found || path != filepath.Clean(plain) {
		t.Fatalf("repaired compressed path = %q found=%v error=%v, want %q", path, found, err, plain)
	}
}

func assertThreadArchiveRow(t *testing.T, runtime *StateRuntime, threadID, wantPath string, wantArchived bool, wantArchivedAt sql.NullInt64) {
	t.Helper()
	var path string
	var archived bool
	var archivedAt sql.NullInt64
	var recency, recencyMS int64
	if err := runtime.StateDB().QueryRow(`SELECT rollout_path, archived, archived_at, recency_at, recency_at_ms FROM threads WHERE id = ?`, threadID).Scan(&path, &archived, &archivedAt, &recency, &recencyMS); err != nil {
		t.Fatal(err)
	}
	if path != filepath.Clean(wantPath) || archived != wantArchived || archivedAt != wantArchivedAt || recency != 123 || recencyMS != 123456 {
		t.Fatalf("archive row = path:%q archived:%v archived_at:%v recency:%d/%d", path, archived, archivedAt, recency, recencyMS)
	}
}

func assertTableThreadCount(t *testing.T, db *sql.DB, table, column, threadID string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+column+` = ?`, threadID).Scan(&count); err != nil || count != want {
		t.Fatalf("%s count = %d, %v; want %d", table, count, err, want)
	}
}
