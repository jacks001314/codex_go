package session

import (
	"errors"
	"testing"
	"time"
)

func TestListItemsSupportsIncrementalUpdatedOrdinalReplay(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	record := &Record{
		ID:        "thread-items",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Items: []Item{
			{ID: "item-1", Type: "message", Text: "updated", CreatedAtOrdinal: 1, UpdatedAtOrdinal: 4, Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "item-2", Type: "message", Text: "second", CreatedAtOrdinal: 2, UpdatedAtOrdinal: 2, Metadata: map[string]any{"turnId": "turn-2"}},
			{ID: "item-3", Type: "message", Text: "third", CreatedAtOrdinal: 3, UpdatedAtOrdinal: 3, Metadata: map[string]any{"turnId": "turn-2"}},
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}

	watermark := uint64(0)
	first, err := store.ListItems(record.ID, ListItemsOptions{
		PageSize:              2,
		SortKey:               ItemSortUpdatedAtOrdinal,
		AfterUpdatedAtOrdinal: &watermark,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := replayItemIDs(first.Items); len(got) != 2 || got[0] != "item-2" || got[1] != "item-3" {
		t.Fatalf("first update page IDs = %v", got)
	}
	second, err := store.ListItems(record.ID, ListItemsOptions{
		Cursor:                first.NextCursor,
		PageSize:              2,
		SortKey:               ItemSortUpdatedAtOrdinal,
		AfterUpdatedAtOrdinal: &watermark,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := replayItemIDs(second.Items); len(got) != 1 || got[0] != "item-1" {
		t.Fatalf("second update page IDs = %v", got)
	}

	exclusive := uint64(2)
	created, err := store.ListItems(record.ID, ListItemsOptions{
		TurnID:                "turn-2",
		SortKey:               ItemSortCreatedAtOrdinal,
		AfterUpdatedAtOrdinal: &exclusive,
		SortDirection:         SortDesc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := replayItemIDs(created.Items); len(got) != 1 || got[0] != "item-3" {
		t.Fatalf("filtered creation page IDs = %v", got)
	}
	if _, err := store.ListItems(record.ID, ListItemsOptions{
		Cursor:  first.NextCursor,
		SortKey: ItemSortCreatedAtOrdinal,
	}); !errors.Is(err, ErrInvalidThreadID) {
		t.Fatalf("cross-scope cursor error = %v", err)
	}
	if _, err := store.ListItems(record.ID, ListItemsOptions{
		SortKey: ItemSortUpdatedAtOrdinal,
	}); !errors.Is(err, ErrInvalidThreadID) {
		t.Fatalf("missing watermark error = %v", err)
	}
}

func TestListItemsRejectsIncrementalReplayForFork(t *testing.T) {
	store := NewStore(t.TempDir())
	record := &Record{ID: "child", ForkedFromID: "parent", ParentThreadID: "parent", CreatedAt: time.Now().UTC()}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	watermark := uint64(0)
	if _, err := store.ListItems(record.ID, ListItemsOptions{AfterUpdatedAtOrdinal: &watermark}); !errors.Is(err, ErrInvalidThreadID) {
		t.Fatalf("fork incremental replay error = %v", err)
	}
}

func replayItemIDs(items []Item) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].ID
	}
	return out
}
