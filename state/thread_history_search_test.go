package state

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/rollout"
)

func TestThreadHistorySearchMatchesRustVisibilityPaginationAndUTF16Ranges(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	path := writePaginatedHistoryFixture(t, home, "thread-search", nil, []historyFixtureTurn{{
		ID: "turn-1",
		Items: []historyFixtureItem{
			{ID: "user-1", Type: "userMessage", Text: "context ## My request for Codex: 😀 Needle one NEEDLE"},
			{ID: "commentary-1", Type: "agentMessage", Text: "needle hidden", Phase: "commentary"},
			{ID: "final-1", Type: "agentMessage", Text: "# Answer\n\n**Needle** `code`", Phase: "final_answer"},
		},
	}})
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	first, err := runtime.SearchThreadHistoryOccurrences(ctx, ThreadHistorySearchOccurrencesParams{
		ThreadID: "thread-search", SearchTerm: "needle", PageSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].ItemID != "user-1" || first.Items[0].Snippet != "😀 Needle one NEEDLE" {
		t.Fatalf("first search page = %#v", first)
	}
	if got := first.Items[0].SnippetMatchRange; got.Start != 3 || got.End != 9 {
		t.Fatalf("first UTF-16 range = %#v", got)
	}
	assertSearchCursor(t, first.NextCursor, "thread-search", "needle", 1)
	assertHistoryCursor(t, first.Items[0].TurnCursor, "thread-search", historyCursorTurns, true)

	second, err := runtime.SearchThreadHistoryOccurrences(ctx, ThreadHistorySearchOccurrencesParams{
		ThreadID: "thread-search", SearchTerm: "needle", Cursor: first.NextCursor, PageSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ItemID != "user-1" {
		t.Fatalf("second search page = %#v", second)
	}
	if got := second.Items[0].SnippetMatchRange; got.Start != 14 || got.End != 20 {
		t.Fatalf("second UTF-16 range = %#v", got)
	}
	assertSearchCursor(t, second.NextCursor, "thread-search", "needle", 0)

	third, err := runtime.SearchThreadHistoryOccurrences(ctx, ThreadHistorySearchOccurrencesParams{
		ThreadID: "thread-search", SearchTerm: "needle", Cursor: second.NextCursor, PageSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Items) != 1 || third.Items[0].ItemID != "final-1" || third.Items[0].Snippet != "Answer Needle code" || third.NextCursor != nil {
		t.Fatalf("third search page = %#v", third)
	}

	wrongTerm := *first.NextCursor
	if _, err := runtime.SearchThreadHistoryOccurrences(ctx, ThreadHistorySearchOccurrencesParams{
		ThreadID: "thread-search", SearchTerm: "Needle", Cursor: &wrongTerm, PageSize: 1,
	}); !isInvalidHistoryError(err) {
		t.Fatalf("wrong-term cursor error = %v", err)
	}
}

func TestThreadHistorySearchUsesVisibleForkTurnForNavigation(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	rootPath := writePaginatedHistoryFixture(t, home, "search-root", nil, []historyFixtureTurn{{
		ID: "shared", Items: []historyFixtureItem{{ID: "root-user", Type: "userMessage", Text: "inherited needle"}},
	}})
	if err := runtime.ReconcileRollout(ctx, rootPath, false); err != nil {
		t.Fatal(err)
	}
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := MaterializeThreadHistory(ctx, db, "search-root", rootPath, 0, nil); err != nil {
		t.Fatal(err)
	}
	var endOrdinal, endOffset int64
	if err := db.QueryRowContext(ctx, `SELECT rollout_end_ordinal, rollout_end_byte_offset FROM thread_turns WHERE thread_id = ? AND turn_id = ?`, "search-root", "shared").Scan(&endOrdinal, &endOffset); err != nil {
		t.Fatal(err)
	}
	base := &rollout.HistoryPosition{ThreadID: "search-root", EndOrdinalExclusive: uint64(endOrdinal + 1), EndByteOffset: uint64(endOffset)}
	childPath := writePaginatedHistoryFixture(t, home, "search-child", base, []historyFixtureTurn{{ID: "shared"}})
	if err := runtime.ReconcileRollout(ctx, childPath, false); err != nil {
		t.Fatal(err)
	}

	page, err := runtime.SearchThreadHistoryOccurrences(ctx, ThreadHistorySearchOccurrencesParams{
		ThreadID: "search-child", SearchTerm: "needle", PageSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ItemID != "root-user" {
		t.Fatalf("inherited search page = %#v", page)
	}
	var cursor historyCursor
	if err := json.Unmarshal([]byte(page.Items[0].TurnCursor), &cursor); err != nil {
		t.Fatal(err)
	}
	var childTurnOrdinal int64
	if err := db.QueryRowContext(ctx, `SELECT rollout_ordinal FROM thread_turns WHERE thread_id = ? AND turn_id = ?`, "search-child", "shared").Scan(&childTurnOrdinal); err != nil {
		t.Fatal(err)
	}
	if cursor.RolloutOrdinal != uint64(childTurnOrdinal) {
		t.Fatalf("visible turn cursor ordinal = %d, want %d", cursor.RolloutOrdinal, childTurnOrdinal)
	}
}

func assertSearchCursor(t *testing.T, raw *string, threadID, searchTerm string, occurrenceIndex int) {
	t.Helper()
	if raw == nil {
		t.Fatal("search cursor is nil")
	}
	var cursor threadHistorySearchCursor
	if err := json.Unmarshal([]byte(*raw), &cursor); err != nil {
		t.Fatal(err)
	}
	if cursor.ThreadID != threadID || cursor.SearchTerm != searchTerm || cursor.NextOccurrenceIndex != occurrenceIndex {
		t.Fatalf("search cursor = %#v", cursor)
	}
}
