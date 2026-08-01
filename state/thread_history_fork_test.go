package state

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPreparePaginatedForkUsesPhysicalProjectionBoundaries(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	path := writePaginatedHistoryFixture(t, home, "fork-source", nil, []historyFixtureTurn{
		{ID: "turn-1", Items: []historyFixtureItem{{ID: "user-1", Type: "userMessage", Text: "one"}}},
		{ID: "turn-2", Items: []historyFixtureItem{{ID: "user-2", Type: "userMessage", Text: "two"}}},
		{ID: "turn-active", InProgress: true},
	})
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	latest, err := runtime.PreparePaginatedFork(ctx, "fork-source", ThreadHistoryForkLatest, "")
	if err != nil {
		t.Fatal(err)
	}
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := LoadThreadHistoryProjectionState(ctx, db, "fork-source")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ThreadID != "fork-source" || latest.EndOrdinalExclusive != projection.NextRolloutOrdinal || latest.EndByteOffset != projection.NextRolloutByteOffset {
		t.Fatalf("latest position = %#v, projection = %#v", latest, projection)
	}

	through, err := runtime.PreparePaginatedFork(ctx, "fork-source", ThreadHistoryForkThroughTurn, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	var endOrdinal, endOffset int64
	if err := db.QueryRowContext(ctx, `SELECT rollout_end_ordinal, rollout_end_byte_offset FROM thread_turns WHERE thread_id = ? AND turn_id = ?`, "fork-source", "turn-1").Scan(&endOrdinal, &endOffset); err != nil {
		t.Fatal(err)
	}
	if through == nil || through.EndOrdinalExclusive != uint64(endOrdinal+1) || through.EndByteOffset != uint64(endOffset) {
		t.Fatalf("through position = %#v, row = %d/%d", through, endOrdinal, endOffset)
	}

	before, err := runtime.PreparePaginatedFork(ctx, "fork-source", ThreadHistoryForkBeforeTurn, "turn-2")
	if err != nil {
		t.Fatal(err)
	}
	var startOrdinal, startOffset int64
	if err := db.QueryRowContext(ctx, `SELECT rollout_ordinal, rollout_byte_offset FROM thread_turns WHERE thread_id = ? AND turn_id = ?`, "fork-source", "turn-2").Scan(&startOrdinal, &startOffset); err != nil {
		t.Fatal(err)
	}
	if before == nil || before.EndOrdinalExclusive != uint64(startOrdinal) || before.EndByteOffset != uint64(startOffset) {
		t.Fatalf("before position = %#v, row = %d/%d", before, startOrdinal, startOffset)
	}
	if first, err := runtime.PreparePaginatedFork(ctx, "fork-source", ThreadHistoryForkBeforeTurn, "turn-1"); err != nil || first != nil {
		t.Fatalf("before first turn = %#v, err %v", first, err)
	}
	if _, err := runtime.PreparePaginatedFork(ctx, "fork-source", ThreadHistoryForkThroughTurn, "turn-active"); !isInvalidHistoryError(err) {
		t.Fatalf("through active error = %v", err)
	}
	if _, err := runtime.PreparePaginatedFork(ctx, "fork-source", ThreadHistoryForkThroughTurn, "missing"); !isInvalidHistoryError(err) {
		t.Fatalf("missing turn error = %v", err)
	}

	childPath := writePaginatedHistoryFixture(t, home, "fork-empty-child", through, nil)
	if err := runtime.ReconcileRollout(ctx, childPath, false); err != nil {
		t.Fatal(err)
	}
	childLatest, err := runtime.PreparePaginatedFork(ctx, "fork-empty-child", ThreadHistoryForkLatest, "")
	if err != nil {
		t.Fatal(err)
	}
	if childLatest == nil || *childLatest != *through {
		t.Fatalf("empty child latest = %#v, want collapsed %#v", childLatest, through)
	}
}

func TestPreparePaginatedForkMaterializesCompressedLineageForReference(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	path := writePaginatedHistoryFixture(t, home, "fork-compressed", nil, []historyFixtureTurn{{
		ID: "turn-1", Items: []historyFixtureItem{{ID: "user-1", Type: "userMessage", Text: "compressed"}},
	}})
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	compressed := compressBackfillRollout(t, path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	position, err := runtime.PreparePaginatedFork(ctx, "fork-compressed", ThreadHistoryForkLatest, "")
	if err != nil {
		t.Fatal(err)
	}
	if position == nil || position.ThreadID != "fork-compressed" {
		t.Fatalf("compressed fork position = %#v", position)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plain referenced rollout missing: %v", err)
	}
	if _, err := os.Stat(compressed); !os.IsNotExist(err) {
		t.Fatalf("compressed referenced rollout still exists: %v", err)
	}
}

func TestPreparePaginatedForkBeforeTerminalOnlyTurnRejectsMissingStart(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	now := time.Date(2026, 7, 31, 5, 6, 7, 0, time.UTC)
	path := writePaginatedHistoryFixture(t, home, "fork-terminal-only", nil, nil)
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := MaterializeThreadHistory(ctx, db, "fork-terminal-only", path, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO thread_turns (
thread_id, turn_id, rollout_ordinal, rollout_byte_offset, rollout_end_ordinal, rollout_end_byte_offset, status, started_at, completed_at, duration_ms
) VALUES (?, ?, 1, 100, 1, 200, 'completed', ?, ?, 1000)`, "fork-terminal-only", "terminal-only", now.Unix(), now.Add(time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.PreparePaginatedFork(ctx, "fork-terminal-only", ThreadHistoryForkBeforeTurn, "terminal-only"); !isInvalidHistoryError(err) {
		t.Fatalf("terminal-only before error = %v", err)
	}
}
