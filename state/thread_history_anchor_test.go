package state

import (
	"context"
	"errors"
	"testing"

	"codex_go/rollout"
)

func anchorPosition(itemID string) *ThreadHistoryListItemsPosition {
	return &ThreadHistoryListItemsPosition{Anchor: &ThreadHistoryItemAnchor{ItemID: itemID}}
}

func invalidHistoryMessage(t *testing.T, err error) string {
	t.Helper()
	var historyErr *ThreadHistoryError
	if !errors.As(err, &historyErr) || historyErr.Kind != ThreadHistoryInvalidRequest {
		t.Fatalf("error = %v, want an invalid history request", err)
	}
	return historyErr.Message
}

func newAnchorHistoryRuntime(t *testing.T, threadID string, turns []historyFixtureTurn) *StateRuntime {
	t.Helper()
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
	path := writePaginatedHistoryFixture(t, home, threadID, nil, turns)
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	return runtime
}

// Mirrors Rust #48151: an item anchor is an exclusive position inside the
// requested turn - ascending pages continue after it and descending pages
// continue before it - and the rest of the history follows through the returned
// opaque cursor.
func TestThreadHistoryItemAnchorPaginationMatchesRust(t *testing.T) {
	ctx := context.Background()
	runtime := newAnchorHistoryRuntime(t, "thread-anchor", []historyFixtureTurn{
		{ID: "turn-1", Items: []historyFixtureItem{
			{ID: "a", Type: "userMessage", Text: "a"},
			{ID: "b", Type: "reasoning", Text: "b"},
			{ID: "c", Type: "agentMessage", Text: "c"},
			{ID: "d", Type: "reasoning", Text: "d"},
			{ID: "e", Type: "agentMessage", Text: "e"},
		}},
		{ID: "turn-2", Items: []historyFixtureItem{{ID: "f", Type: "userMessage", Text: "f"}}},
	})
	turnID := "turn-1"

	page, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
		ThreadID: "thread-anchor", TurnID: &turnID, Position: anchorPosition("b"), PageSize: 10, SortDirection: ThreadHistorySortAsc,
	})
	if err != nil || !equalStrings(historyItemIDs(page.Items), []string{"c", "d", "e"}) {
		t.Fatalf("ascending anchor page = %#v, err %v", page, err)
	}

	// The anchor is excluded and a page boundary continues with the returned
	// cursor.
	first, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
		ThreadID: "thread-anchor", TurnID: &turnID, Position: anchorPosition("b"), PageSize: 2, SortDirection: ThreadHistorySortAsc,
	})
	if err != nil || !equalStrings(historyItemIDs(first.Items), []string{"c", "d"}) || first.NextCursor == nil {
		t.Fatalf("first anchored page = %#v, err %v", first, err)
	}
	next, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
		ThreadID: "thread-anchor", TurnID: &turnID, Position: &ThreadHistoryListItemsPosition{Cursor: first.NextCursor}, PageSize: 2, SortDirection: ThreadHistorySortAsc,
	})
	if err != nil || !equalStrings(historyItemIDs(next.Items), []string{"e"}) {
		t.Fatalf("continued anchored page = %#v, err %v", next, err)
	}

	descending, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
		ThreadID: "thread-anchor", TurnID: &turnID, Position: anchorPosition("c"), PageSize: 10, SortDirection: ThreadHistorySortDesc,
	})
	if err != nil || !equalStrings(historyItemIDs(descending.Items), []string{"b", "a"}) {
		t.Fatalf("descending anchor page = %#v, err %v", descending, err)
	}
}

// Mirrors Rust #48151's scope validation: the anchor must identify an item in
// the requested turn's visible history, and an anchor always needs a turn id.
func TestThreadHistoryItemAnchorScopeValidationMatchesRust(t *testing.T) {
	ctx := context.Background()
	runtime := newAnchorHistoryRuntime(t, "thread-anchor-scope", []historyFixtureTurn{
		{ID: "turn-1", Items: []historyFixtureItem{{ID: "a", Type: "userMessage", Text: "a"}}},
		{ID: "turn-2", Items: []historyFixtureItem{{ID: "b", Type: "userMessage", Text: "b"}}},
	})
	turnID := "turn-1"
	otherTurn := "turn-2"

	// An item that belongs to another turn is out of scope.
	_, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
		ThreadID: "thread-anchor-scope", TurnID: &otherTurn, Position: anchorPosition("a"), PageSize: 1, SortDirection: ThreadHistorySortAsc,
	})
	if message := invalidHistoryMessage(t, err); message != "cursor.itemId does not identify an item in the requested history scope" {
		t.Fatalf("out-of-scope anchor message = %q", message)
	}
	// Unknown and empty item ids are rejected the same way.
	for _, itemID := range []string{"missing", ""} {
		_, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
			ThreadID: "thread-anchor-scope", TurnID: &turnID, Position: anchorPosition(itemID), PageSize: 1, SortDirection: ThreadHistorySortAsc,
		})
		if message := invalidHistoryMessage(t, err); message != "cursor.itemId does not identify an item in the requested history scope" {
			t.Fatalf("anchor %q message = %q", itemID, message)
		}
	}
	// The anchor needs a non-empty turn id.
	emptyTurn := ""
	for _, turn := range []*string{nil, &emptyTurn} {
		_, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
			ThreadID: "thread-anchor-scope", TurnID: turn, Position: anchorPosition("a"), PageSize: 1, SortDirection: ThreadHistorySortAsc,
		})
		if message := invalidHistoryMessage(t, err); message != "turnId is required when cursor is an item anchor" {
			t.Fatalf("missing turn id message = %q", message)
		}
	}
}

// An anchor resolves inside the thread's whole visible history, so an inherited
// fork segment answers for the child thread as well.
func TestThreadHistoryItemAnchorResolvesInheritedForkHistory(t *testing.T) {
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

	rootPath := writePaginatedHistoryFixture(t, home, "anchor-root", nil, []historyFixtureTurn{
		{ID: "root-1", Items: []historyFixtureItem{
			{ID: "root-user", Type: "userMessage", Text: "root"},
			{ID: "root-agent", Type: "agentMessage", Text: "answer"},
		}},
	})
	if err := runtime.ReconcileRollout(ctx, rootPath, false); err != nil {
		t.Fatal(err)
	}
	rootDB, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := MaterializeThreadHistory(ctx, rootDB, "anchor-root", rootPath, 0, nil); err != nil {
		t.Fatal(err)
	}
	var endOrdinal, endOffset int64
	if err := rootDB.QueryRowContext(ctx,
		`SELECT rollout_end_ordinal, rollout_end_byte_offset FROM thread_turns WHERE thread_id = ? AND turn_id = ?`,
		"anchor-root", "root-1").Scan(&endOrdinal, &endOffset); err != nil {
		t.Fatal(err)
	}
	base := &rollout.HistoryPosition{ThreadID: "anchor-root", EndOrdinalExclusive: uint64(endOrdinal + 1), EndByteOffset: uint64(endOffset)}
	childPath := writePaginatedHistoryFixture(t, home, "anchor-child", base, []historyFixtureTurn{
		{ID: "child-1", Items: []historyFixtureItem{{ID: "child-item", Type: "userMessage", Text: "child"}}},
	})
	if err := runtime.ReconcileRollout(ctx, childPath, false); err != nil {
		t.Fatal(err)
	}

	rootTurn := "root-1"
	page, err := runtime.ListThreadHistoryItems(ctx, ThreadHistoryListItemsParams{
		ThreadID: "anchor-child", TurnID: &rootTurn, Position: anchorPosition("root-user"), PageSize: 10, SortDirection: ThreadHistorySortAsc,
	})
	if err != nil || !equalStrings(historyItemIDs(page.Items), []string{"root-agent"}) {
		t.Fatalf("inherited anchor page = %#v, err %v", page, err)
	}
}
