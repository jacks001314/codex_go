package state

// Mirrors Rust's realtime_stream_tests.rs: fixed snapshots, late-event
// catch-up, projection coverage, exact event IDs, and fork position bounds.

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

func openRealtimeHistoryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenThreadHistoryDB(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertRealtimeHistoryTestItem(t *testing.T, db *sql.DB, threadID string, position int64, observedAtMS *int64) {
	t.Helper()
	item, err := json.Marshal(map[string]any{
		"id":                  "event-" + string(rune('0'+position)),
		"realtime_session_id": "voice",
		"observed_at_ms":      observedAtMS,
		"type":                "realtime_session_started",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO thread_realtime_items (thread_id, item_id, rollout_ordinal, created_at_ms, item_type, item_json)
VALUES (?, ?, ?, 0, 'realtime_session_started', ?)`, threadID, "event-"+string(rune('0'+position)), position, string(item)); err != nil {
		t.Fatal(err)
	}
}

func realtimeObserved(value int64) *int64 { return &value }

func TestRealtimeSnapshotStaysFixedWhilePositionCatchupFindsLateSealLikeRust(t *testing.T) {
	ctx := context.Background()
	db := openRealtimeHistoryTestDB(t)
	threadID := "018f0000-0000-7000-8000-000000000001"
	insertRealtimeHistoryTestItem(t, db, threadID, 0, realtimeObserved(200))
	insertRealtimeHistoryTestItem(t, db, threadID, 1, realtimeObserved(200))
	insertRealtimeHistoryTestItem(t, db, threadID, 2, realtimeObserved(100))
	params := ThreadRealtimeListParams{ThreadID: threadID, Order: ThreadRealtimeOrderObservedAt, PageSize: 1}
	first, err := readThreadRealtimePage(ctx, db, params, threadID, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 1 || first.Data[0].Position != 2 || first.SnapshotEnd != 3 || first.NextCursor == nil {
		t.Fatalf("first page = %+v", first)
	}

	insertRealtimeHistoryTestItem(t, db, threadID, 3, realtimeObserved(50))
	nextParams := params
	nextParams.Cursor = first.NextCursor
	next, err := readThreadRealtimePage(ctx, db, nextParams, threadID, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Data) != 1 || next.Data[0].Position != 0 || next.SnapshotEnd != 3 || next.NextCursor == nil {
		t.Fatalf("next page = %+v", next)
	}
	lastParams := params
	lastParams.Cursor = next.NextCursor
	last, err := readThreadRealtimePage(ctx, db, lastParams, threadID, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Data) != 1 || last.Data[0].Position != 1 || last.NextCursor != nil {
		t.Fatalf("last page = %+v", last)
	}

	catchupParams := ThreadRealtimeListParams{
		ThreadID:      threadID,
		Order:         ThreadRealtimeOrderPosition,
		AfterPosition: pointerUint64(first.SnapshotEnd - 1),
		PageSize:      1,
	}
	catchup, err := readThreadRealtimePage(ctx, db, catchupParams, threadID, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(catchup.Data) != 1 || catchup.Data[0].Position != 3 {
		t.Fatalf("catch-up page = %+v", catchup)
	}

	session := "another-voice"
	wrongParams := params
	wrongParams.Cursor = first.NextCursor
	wrongParams.SessionID = &session
	if _, err := readThreadRealtimePage(ctx, db, wrongParams, threadID, 0, 4); !isInvalidHistoryError(err) {
		t.Fatalf("wrong-query cursor error = %v", err)
	}
}

func pointerUint64(value uint64) *uint64 { return &value }

func TestRealtimeProjectionLagIsExplicitAndDescendantsHideParentPositionsLikeRust(t *testing.T) {
	ctx := context.Background()
	db := openRealtimeHistoryTestDB(t)
	threadID := "018f0000-0000-7000-8000-000000000002"
	insertRealtimeHistoryTestItem(t, db, threadID, 1, realtimeObserved(100))
	params := ThreadRealtimeListParams{
		ThreadID:                threadID,
		Order:                   ThreadRealtimeOrderObservedAt,
		PageSize:                1,
		RequiredThroughPosition: pointerUint64(2),
	}
	pending, err := readThreadRealtimePage(ctx, db, params, threadID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Coverage != ThreadRealtimeCoveragePending || len(pending.Data) != 0 {
		t.Fatalf("pending page = %+v", pending)
	}
	hiddenParams := ThreadRealtimeListParams{ThreadID: threadID, Order: ThreadRealtimeOrderPosition, PageSize: 1}
	hidden, err := readThreadRealtimePage(ctx, db, hiddenParams, threadID, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden.Data) != 0 {
		t.Fatalf("descendant page = %+v", hidden)
	}
}

func TestRealtimeExactEventIDWaitsForProjectedSealLikeRust(t *testing.T) {
	ctx := context.Background()
	db := openRealtimeHistoryTestDB(t)
	threadID := "018f0000-0000-7000-8000-000000000003"
	eventID := "event-1"
	params := ThreadRealtimeListParams{
		ThreadID: threadID,
		Order:    ThreadRealtimeOrderObservedAt,
		EventID:  &eventID,
		PageSize: 1,
	}
	pending, err := readThreadRealtimePage(ctx, db, params, threadID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Coverage != ThreadRealtimeCoveragePending || pending.SnapshotEnd != 1 || pending.ProjectedThroughItemIndex != 1 {
		t.Fatalf("missing event page = %+v", pending)
	}

	insertRealtimeHistoryTestItem(t, db, threadID, 1, realtimeObserved(100))
	stale, err := readThreadRealtimePage(ctx, db, params, threadID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Coverage != ThreadRealtimeCoveragePending || len(stale.Data) != 0 {
		t.Fatalf("stale projection page = %+v", stale)
	}

	explicitSnapshot := params
	explicitSnapshot.SnapshotEnd = pointerUint64(1)
	excluded, err := readThreadRealtimePage(ctx, db, explicitSnapshot, threadID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if excluded.Coverage != ThreadRealtimeCoverageReady || len(excluded.Data) != 0 {
		t.Fatalf("explicit snapshot page = %+v", excluded)
	}
	from := int64(200)
	filtered := params
	filtered.FromObservedAtMS = &from
	excluded, err = readThreadRealtimePage(ctx, db, filtered, threadID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if excluded.Coverage != ThreadRealtimeCoverageReady || len(excluded.Data) != 0 {
		t.Fatalf("time-filtered page = %+v", excluded)
	}

	ready, err := readThreadRealtimePage(ctx, db, params, threadID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Coverage != ThreadRealtimeCoverageReady || len(ready.Data) != 1 || ready.Data[0].Position != 1 || ready.SnapshotEnd != 2 {
		t.Fatalf("ready event page = %+v", ready)
	}
}
