package state

import (
	"context"
	"os"
	"testing"

	"codex_go/rollout"
)

// TestThreadHistoryNewestPagesSkipUnusedAncestorsLikeRust covers Rust #61840:
// a descending page satisfied by the newest segment must not open an unused
// ancestor, while a page that exactly fills with local rows still probes the
// ancestor for the next-page cursor.
func TestThreadHistoryNewestPagesSkipUnusedAncestorsLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	parentPath := writePaginatedHistoryFixture(t, home, "lazy-parent", nil, []historyFixtureTurn{
		{ID: "parent-turn", Items: []historyFixtureItem{{ID: "parent-item", Type: "userMessage", Text: "parent"}}},
	})
	if err := runtime.ReconcileRollout(ctx, parentPath, false); err != nil {
		t.Fatal(err)
	}
	base := paginatedHistoryBase(t, ctx, runtime, "lazy-parent", parentPath)
	childPath := writePaginatedHistoryFixture(t, home, "lazy-child", base, []historyFixtureTurn{
		{ID: "child-one", Items: []historyFixtureItem{{ID: "child-item-1", Type: "userMessage", Text: "one"}}},
		{ID: "child-two", Items: []historyFixtureItem{{ID: "child-item-2", Type: "userMessage", Text: "two"}}},
		{ID: "child-three", Items: []historyFixtureItem{{ID: "child-item-3", Type: "userMessage", Text: "three"}}},
	})
	if err := runtime.ReconcileRollout(ctx, childPath, false); err != nil {
		t.Fatal(err)
	}

	turnParams := ThreadHistoryListTurnsParams{ThreadID: "lazy-child", PageSize: 2, SortDirection: ThreadHistorySortDesc}
	itemParams := ThreadHistoryListItemsParams{ThreadID: "lazy-child", PageSize: 2, SortDirection: ThreadHistorySortDesc}
	expectedTurns, err := runtime.ListThreadHistoryTurns(ctx, turnParams)
	if err != nil {
		t.Fatalf("turns before ancestor loss: %v", err)
	}
	expectedItems, err := runtime.ListThreadHistoryItems(ctx, itemParams)
	if err != nil {
		t.Fatalf("items before ancestor loss: %v", err)
	}
	if got := historyTurnIDs(expectedTurns.Turns); !equalStrings(got, []string{"child-three", "child-two"}) {
		t.Fatalf("newest turns = %v", got)
	}

	if err := os.Remove(parentPath); err != nil {
		t.Fatal(err)
	}
	turns, err := runtime.ListThreadHistoryTurns(ctx, turnParams)
	if err != nil {
		t.Fatalf("newest turns opened an unused ancestor: %v", err)
	}
	if !equalStrings(historyTurnIDs(turns.Turns), historyTurnIDs(expectedTurns.Turns)) {
		t.Fatalf("turns after ancestor loss = %v", historyTurnIDs(turns.Turns))
	}
	items, err := runtime.ListThreadHistoryItems(ctx, itemParams)
	if err != nil {
		t.Fatalf("newest items opened an unused ancestor: %v", err)
	}
	if !equalStrings(historyItemIDs(items.Items), historyItemIDs(expectedItems.Items)) {
		t.Fatalf("items after ancestor loss = %v", historyItemIDs(items.Items))
	}

	// At exactly the local row count the reader must discover whether an
	// ancestor contributes another row instead of returning a terminal cursor.
	turnParams.PageSize = 3
	if _, err := runtime.ListThreadHistoryTurns(ctx, turnParams); err == nil {
		t.Fatal("page sized to the leaf row count skipped the ancestor lookahead")
	}
}

// TestThreadHistoryDescendingPagesShadowParentTurnsLikeRust covers the
// continuation half of Rust #61840: resuming in an ancestor still lets the
// leaf's copy of a turn shadow the ancestor's row, and the grandparent stays
// unread.
func TestThreadHistoryDescendingPagesShadowParentTurnsLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	rootPath := writePaginatedHistoryFixture(t, home, "shadow-root", nil, []historyFixtureTurn{
		{ID: "shared-turn", Items: []historyFixtureItem{{ID: "root-item", Type: "userMessage", Text: "root"}}},
	})
	if err := runtime.ReconcileRollout(ctx, rootPath, false); err != nil {
		t.Fatal(err)
	}
	parentBase := paginatedHistoryBase(t, ctx, runtime, "shadow-root", rootPath)
	parentPath := writePaginatedHistoryFixture(t, home, "shadow-parent", parentBase, []historyFixtureTurn{
		{ID: "oldest-turn", Items: []historyFixtureItem{{ID: "parent-item-oldest", Type: "userMessage", Text: "oldest"}}},
		{ID: "older-turn", Items: []historyFixtureItem{{ID: "parent-item-older", Type: "userMessage", Text: "older"}}},
		{ID: "shared-turn", Items: []historyFixtureItem{{ID: "parent-item-shared", Type: "userMessage", Text: "shared"}}},
		{ID: "middle-turn", Items: []historyFixtureItem{{ID: "parent-item-middle", Type: "userMessage", Text: "middle"}}},
		{ID: "newest-turn", Items: []historyFixtureItem{{ID: "parent-item-newest", Type: "userMessage", Text: "newest"}}},
	})
	if err := runtime.ReconcileRollout(ctx, parentPath, false); err != nil {
		t.Fatal(err)
	}
	childBase := paginatedHistoryBase(t, ctx, runtime, "shadow-parent", parentPath)
	childPath := writePaginatedHistoryFixture(t, home, "shadow-leaf", childBase, []historyFixtureTurn{
		{ID: "shared-turn", Aborted: true, Items: []historyFixtureItem{{ID: "leaf-item-shared", Type: "reasoning", Text: "leaf"}}},
	})
	if err := runtime.ReconcileRollout(ctx, childPath, false); err != nil {
		t.Fatal(err)
	}

	// An interrupted leaf turn without its own user/agent items keeps the eager
	// inherited-summary lookup: the original summary lives in the grandparent.
	summary, err := runtime.ListThreadHistoryTurns(ctx, ThreadHistoryListTurnsParams{
		ThreadID: "shadow-leaf", PageSize: 1, SortDirection: ThreadHistorySortDesc, ItemsView: ThreadHistoryItemsSummary,
	})
	if err != nil {
		t.Fatalf("inherited summary: %v", err)
	}
	if len(summary.Turns) != 1 || !equalStrings(historyItemIDs(summary.Turns[0].Items), []string{"root-item"}) {
		t.Fatalf("inherited summary = %#v", summary.Turns)
	}

	// The grandparent is never needed once the leaf and parent fill the page.
	if err := os.Remove(rootPath); err != nil {
		t.Fatal(err)
	}
	params := ThreadHistoryListTurnsParams{ThreadID: "shadow-leaf", PageSize: 2, SortDirection: ThreadHistorySortDesc}
	first, err := runtime.ListThreadHistoryTurns(ctx, params)
	if err != nil {
		t.Fatalf("first descending page: %v", err)
	}
	if got := historyTurnIDs(first.Turns); !equalStrings(got, []string{"shared-turn", "newest-turn"}) {
		t.Fatalf("first page = %v", got)
	}
	if first.NextCursor == nil {
		t.Fatal("first page must continue into the ancestor")
	}
	params.Cursor = first.NextCursor
	second, err := runtime.ListThreadHistoryTurns(ctx, params)
	if err != nil {
		t.Fatalf("second descending page: %v", err)
	}
	if got := historyTurnIDs(second.Turns); !equalStrings(got, []string{"middle-turn", "older-turn"}) {
		t.Fatalf("second page = %v", got)
	}
	if second.NextCursor == nil {
		t.Fatal("second page must keep a next cursor")
	}

	itemParams := ThreadHistoryListItemsParams{ThreadID: "shadow-leaf", PageSize: 2, SortDirection: ThreadHistorySortDesc}
	firstItems, err := runtime.ListThreadHistoryItems(ctx, itemParams)
	if err != nil {
		t.Fatalf("first descending item page: %v", err)
	}
	if got := historyItemIDs(firstItems.Items); !equalStrings(got, []string{"leaf-item-shared", "parent-item-newest"}) {
		t.Fatalf("first item page = %v", got)
	}
	itemParams.Position = &ThreadHistoryListItemsPosition{Cursor: firstItems.NextCursor}
	secondItems, err := runtime.ListThreadHistoryItems(ctx, itemParams)
	if err != nil {
		t.Fatalf("second descending item page: %v", err)
	}
	if got := historyItemIDs(secondItems.Items); !equalStrings(got, []string{"parent-item-middle", "parent-item-shared"}) {
		t.Fatalf("second item page = %v", got)
	}
}

// paginatedHistoryBase freezes a parent rollout at its own frontier so a child
// fixture can inherit it.
func paginatedHistoryBase(t *testing.T, ctx context.Context, runtime *StateRuntime, threadID, path string) *rollout.HistoryPosition {
	t.Helper()
	db := mustThreadHistoryDB(t, runtime, ctx)
	if err := MaterializeThreadHistory(ctx, db, threadID, path, 0, nil); err != nil {
		t.Fatal(err)
	}
	projection, err := LoadThreadHistoryProjectionState(ctx, db, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if projection == nil {
		t.Fatalf("missing projection state for %s", threadID)
	}
	return &rollout.HistoryPosition{
		ThreadID:            threadID,
		EndOrdinalExclusive: projection.NextRolloutOrdinal,
		EndByteOffset:       projection.NextRolloutByteOffset,
	}
}
