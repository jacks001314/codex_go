package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/rollout"
)

func TestThreadHistoryQueriesPageProjectedRowsLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	path := writePaginatedHistoryFixture(t, home, "thread-page", nil, []historyFixtureTurn{
		{ID: "turn-1", Items: []historyFixtureItem{
			{ID: "user-1", Type: "userMessage", Text: "question"},
			{ID: "middle-1", Type: "reasoning", Text: "thinking"},
			{ID: "agent-1", Type: "agentMessage", Text: "answer", Phase: "final_answer"},
		}},
		{ID: "turn-2", Items: []historyFixtureItem{{ID: "user-2", Type: "userMessage", Text: "next"}}},
		{ID: "turn-3", InProgress: true},
	})
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	turns, err := runtime.ListThreadHistoryTurns(ctx, ThreadHistoryListTurnsParams{
		ThreadID: "thread-page", PageSize: 2, SortDirection: ThreadHistorySortAsc, ItemsView: ThreadHistoryItemsSummary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := historyTurnIDs(turns.Turns); !equalStrings(got, []string{"turn-1", "turn-2"}) {
		t.Fatalf("first turns = %v", got)
	}
	if got := historyItemIDs(turns.Turns[0].Items); !equalStrings(got, []string{"user-1", "agent-1"}) {
		t.Fatalf("summary items = %v", got)
	}
	if turns.NextCursor == nil || turns.BackwardsCursor == nil {
		t.Fatalf("turn cursors = next %v backwards %v", turns.NextCursor, turns.BackwardsCursor)
	}
	assertHistoryCursor(t, *turns.NextCursor, "thread-page", historyCursorTurns, false)

	nextTurns, err := runtime.ListThreadHistoryTurns(ctx, ThreadHistoryListTurnsParams{
		ThreadID: "thread-page", Cursor: turns.NextCursor, PageSize: 2, SortDirection: ThreadHistorySortAsc, ItemsView: ThreadHistoryItemsNotLoaded,
	})
	if err != nil || !equalStrings(historyTurnIDs(nextTurns.Turns), []string{"turn-3"}) {
		t.Fatalf("next turns = %#v, err %v", nextTurns, err)
	}
	backwardsTurns, err := runtime.ListThreadHistoryTurns(ctx, ThreadHistoryListTurnsParams{
		ThreadID: "thread-page", Cursor: nextTurns.BackwardsCursor, PageSize: 2, SortDirection: ThreadHistorySortDesc, ItemsView: ThreadHistoryItemsNotLoaded,
	})
	if err != nil || !equalStrings(historyTurnIDs(backwardsTurns.Turns), []string{"turn-3", "turn-2"}) {
		t.Fatalf("backwards turns = %#v, err %v", backwardsTurns, err)
	}

	items, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
		ThreadID: "thread-page", PageSize: 2, SortDirection: ThreadHistorySortAsc,
	})
	if err != nil || !equalStrings(historyItemIDs(items.Items), []string{"user-1", "middle-1"}) {
		t.Fatalf("first items = %#v, err %v", items, err)
	}
	if items.NextCursor == nil || items.BackwardsCursor == nil {
		t.Fatalf("item cursors = next %v backwards %v", items.NextCursor, items.BackwardsCursor)
	}
	assertHistoryCursor(t, *items.NextCursor, "thread-page", historyCursorItems, false)
	nextItems, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
		ThreadID: "thread-page", Cursor: items.NextCursor, PageSize: 2, SortDirection: ThreadHistorySortAsc,
	})
	if err != nil || !equalStrings(historyItemIDs(nextItems.Items), []string{"agent-1", "user-2"}) {
		t.Fatalf("next items = %#v, err %v", nextItems, err)
	}
	turnID := "turn-1"
	turnItems, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
		ThreadID: "thread-page", TurnID: &turnID, PageSize: 2, SortDirection: ThreadHistorySortDesc,
	})
	if err != nil || !equalStrings(historyItemIDs(turnItems.Items), []string{"agent-1", "middle-1"}) {
		t.Fatalf("turn items = %#v, err %v", turnItems, err)
	}

	wrongThreadCursor := `{"requestedThreadId":"other","rolloutOrdinal":1,"includeAnchor":false,"scope":{"kind":"turns"}}`
	if _, err := runtime.ListThreadHistoryTurns(ctx, ThreadHistoryListTurnsParams{ThreadID: "thread-page", Cursor: &wrongThreadCursor, PageSize: 1}); !isInvalidHistoryError(err) {
		t.Fatalf("wrong-thread cursor error = %v", err)
	}
	if _, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{ThreadID: "thread-page", Cursor: turns.NextCursor, PageSize: 1}); !isInvalidHistoryError(err) {
		t.Fatalf("wrong-scope cursor error = %v", err)
	}
}

func TestThreadHistoryQueriesProjectCompressedRolloutInJSONLCoordinates(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	path := writePaginatedHistoryFixture(t, home, "query-compressed", nil, []historyFixtureTurn{{
		ID: "turn-1", Items: []historyFixtureItem{{ID: "user-1", Type: "userMessage", Text: "compressed history"}},
	}})
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	compressed := compressBackfillRollout(t, path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	turns, err := runtime.ListThreadHistoryTurns(ctx, ThreadHistoryListTurnsParams{
		ThreadID: "query-compressed", PageSize: 10, SortDirection: ThreadHistorySortAsc, ItemsView: ThreadHistoryItemsSummary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(turns.Turns) != 1 || turns.Turns[0].TurnID != "turn-1" || len(turns.Turns[0].Items) != 1 {
		t.Fatalf("compressed turns = %#v", turns)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("ordinary query materialized plain rollout: %v", err)
	}
	if _, err := os.Stat(compressed); err != nil {
		t.Fatalf("compressed rollout missing after ordinary query: %v", err)
	}
	projection, err := LoadThreadHistoryProjectionState(ctx, mustThreadHistoryDB(t, runtime, ctx), "query-compressed")
	if err != nil || projection == nil {
		t.Fatalf("compressed projection = %#v, %v", projection, err)
	}
	if size, err := rollout.RolloutByteLength(compressed); err != nil || projection.NextRolloutByteOffset != size {
		t.Fatalf("compressed projection offset = %d, size %d, err %v", projection.NextRolloutByteOffset, size, err)
	}
}

func mustThreadHistoryDB(t *testing.T, runtime *StateRuntime, ctx context.Context) *sql.DB {
	t.Helper()
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestThreadHistoryQueriesFollowForkLineageAndBoundSource(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	rootPath := writePaginatedHistoryFixture(t, home, "thread-root", nil, []historyFixtureTurn{
		{ID: "root-1", Items: []historyFixtureItem{{ID: "root-user", Type: "userMessage", Text: "root"}}},
		{ID: "excluded-root", Items: []historyFixtureItem{{ID: "excluded-item", Type: "userMessage", Text: "later"}}},
	})
	if err := runtime.ReconcileRollout(ctx, rootPath, false); err != nil {
		t.Fatal(err)
	}
	rootDB, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := MaterializeThreadHistory(ctx, rootDB, "thread-root", rootPath, 0, nil); err != nil {
		t.Fatal(err)
	}
	var endOrdinal, endOffset int64
	if err := rootDB.QueryRowContext(ctx, `SELECT rollout_end_ordinal, rollout_end_byte_offset FROM thread_turns WHERE thread_id = ? AND turn_id = ?`, "thread-root", "root-1").Scan(&endOrdinal, &endOffset); err != nil {
		t.Fatal(err)
	}
	base := &rollout.HistoryPosition{ThreadID: "thread-root", EndOrdinalExclusive: uint64(endOrdinal + 1), EndByteOffset: uint64(endOffset)}
	childPath := writePaginatedHistoryFixture(t, home, "thread-child", base, []historyFixtureTurn{
		{ID: "child-1", Items: []historyFixtureItem{{ID: "child-item", Type: "userMessage", Text: "child"}}},
	})
	if err := runtime.ReconcileRollout(ctx, childPath, false); err != nil {
		t.Fatal(err)
	}

	turns, err := runtime.ListThreadHistoryTurns(ctx, ThreadHistoryListTurnsParams{
		ThreadID: "thread-child", PageSize: 10, SortDirection: ThreadHistorySortAsc, ItemsView: ThreadHistoryItemsSummary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := historyTurnIDs(turns.Turns); !equalStrings(got, []string{"root-1", "child-1"}) {
		t.Fatalf("lineage turns = %v", got)
	}
	items, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
		ThreadID: "thread-child", PageSize: 10, SortDirection: ThreadHistorySortAsc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := historyItemIDs(items.Items); !equalStrings(got, []string{"root-user", "child-item"}) {
		t.Fatalf("lineage items = %v", got)
	}

	gapCursor := `{"requestedThreadId":"thread-child","rolloutOrdinal":` + uintStringForHistory(base.EndOrdinalExclusive) + `,"includeAnchor":true,"scope":{"kind":"turns"}}`
	if _, err := runtime.ListThreadHistoryTurns(ctx, ThreadHistoryListTurnsParams{ThreadID: "thread-child", Cursor: &gapCursor, PageSize: 1}); !isInvalidHistoryError(err) {
		t.Fatalf("metadata-gap cursor error = %v", err)
	}
}

type historyFixtureTurn struct {
	ID         string
	Items      []historyFixtureItem
	InProgress bool
}

type historyFixtureItem struct {
	ID, Type, Text, Phase string
}

func writePaginatedHistoryFixture(t *testing.T, home, threadID string, base *rollout.HistoryPosition, turns []historyFixtureTurn) string {
	t.Helper()
	now := time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, SessionID: threadID, ThreadID: threadID, CWD: filepath.Join(home, "repo"),
		ModelProvider: "openai", HistoryMode: "paginated", HistoryBase: base, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for turnIndex, turn := range turns {
		startedAt := now.Add(time.Duration(turnIndex) * time.Minute)
		if err := recorder.AppendTurnStarted(turn.ID, startedAt); err != nil {
			t.Fatal(err)
		}
		for itemIndex, item := range turn.Items {
			itemJSON := map[string]any{"type": item.Type, "id": item.ID, "text": item.Text}
			if item.Type == "userMessage" {
				itemJSON["content"] = []map[string]any{{"type": "text", "text": item.Text}}
			}
			if item.Phase != "" {
				itemJSON["phase"] = item.Phase
			}
			payload, err := json.Marshal(map[string]any{
				"type": "item_completed", "thread_id": threadID, "turn_id": turn.ID,
				"started_at_ms":   startedAt.Add(time.Duration(itemIndex) * time.Second).UnixMilli(),
				"completed_at_ms": startedAt.Add(time.Duration(itemIndex+1) * time.Second).UnixMilli(), "item": itemJSON,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: startedAt.Format(time.RFC3339Nano), Payload: payload}); err != nil {
				t.Fatal(err)
			}
		}
		if !turn.InProgress {
			completedAt := startedAt.Add(30 * time.Second)
			if err := recorder.AppendTurnComplete(turn.ID, completedAt, 30_000); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func historyTurnIDs(values []ThreadHistoryTurn) []string {
	out := make([]string, len(values))
	for i := range values {
		out[i] = values[i].TurnID
	}
	return out
}

func historyItemIDs(values []ThreadHistoryItem) []string {
	out := make([]string, len(values))
	for i := range values {
		out[i] = values[i].ItemID
	}
	return out
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func assertHistoryCursor(t *testing.T, raw, threadID, scope string, includeAnchor bool) {
	t.Helper()
	var cursor historyCursor
	if err := json.Unmarshal([]byte(raw), &cursor); err != nil {
		t.Fatal(err)
	}
	if cursor.RequestedThreadID != threadID || cursor.Scope.Kind != scope || cursor.IncludeAnchor != includeAnchor {
		t.Fatalf("cursor = %#v", cursor)
	}
}

func isInvalidHistoryError(err error) bool {
	historyErr, ok := err.(*ThreadHistoryError)
	return ok && historyErr.Kind == ThreadHistoryInvalidRequest
}

func uintStringForHistory(value uint64) string {
	data, _ := json.Marshal(value)
	return string(data)
}
