package state

import (
	"context"
	"testing"
)

// TestThreadHistoryItemIDsFilterMatchesRust pins the store half of Rust #52637:
// an item-id filter returns only the named items, in canonical stored order,
// scoped to the requested turn, while missing ids are simply absent.
func TestThreadHistoryItemIDsFilterMatchesRust(t *testing.T) {
	runtime := newAnchorHistoryRuntime(t, "thread-item-ids", []historyFixtureTurn{
		{ID: "turn-1", Items: []historyFixtureItem{
			{ID: "a", Type: "userMessage", Text: "a"},
			{ID: "b", Type: "reasoning", Text: "b"},
			{ID: "c", Type: "agentMessage", Text: "c"},
		}},
		{ID: "turn-2", Items: []historyFixtureItem{{ID: "d", Type: "userMessage", Text: "d"}}},
	})
	turnID := "turn-1"
	page, err := runtime.ListThreadHistoryItems(context.Background(), ThreadHistoryListItemsParams{
		ThreadID:      "thread-item-ids",
		TurnID:        &turnID,
		ItemIDs:       []string{"c", "a", "missing"},
		PageSize:      100,
		SortDirection: ThreadHistorySortAsc,
	})
	if err != nil {
		t.Fatalf("ListThreadHistoryItems() error = %v", err)
	}
	if got := historyItemIDs(page.Items); len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("filtered items = %#v, want canonical [a c]", got)
	}
}
