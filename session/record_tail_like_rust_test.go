package session

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// saveTailTestRecord stores one record and returns its thread id.
func saveTailTestRecord(t *testing.T, store *Store, threadID ThreadID, items []Item) {
	t.Helper()
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := store.Save(&Record{
		ID:        threadID,
		SessionID: string(threadID),
		Title:     "Tail page",
		Preview:   "tail prompt",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  Metadata{CWD: `D:\repo`, Model: "gpt-5.4", HistoryMode: "legacy"},
		Items:     items,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

func tailTestItems(count int) []Item {
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	items := make([]Item, 0, count)
	for index := 0; index < count; index++ {
		item := Item{
			ID:        fmt.Sprintf("item-%03d", index),
			Type:      "message",
			Role:      "assistant",
			Text:      fmt.Sprintf("answer-%03d", index),
			CreatedAt: now.Add(time.Duration(index) * time.Second),
		}
		if index%9 != 0 {
			item.CreatedAtOrdinal = uint64(index + 1)
		}
		items = append(items, item)
	}
	return items
}

// TestReadTailPageMatchesListItemsLikeRust pins the bounded read to the
// canonical store page: Rust pages the history store for a resume window
// instead of decoding the whole stored transcript, so the fast read must
// return the same metadata, items and cursor.
func TestReadTailPageMatchesListItemsLikeRust(t *testing.T) {
	store := NewStore(t.TempDir())
	threadID := ThreadID("123e4567-e89b-12d3-a456-426614174333")
	saveTailTestRecord(t, store, threadID, tailTestItems(130))

	tail, ok, err := store.ReadTailPage(threadID, 60)
	if err != nil {
		t.Fatalf("ReadTailPage() error = %v", err)
	}
	if !ok {
		t.Fatal("ReadTailPage() fell back for a plain record")
	}
	if tail.TotalItems != 130 {
		t.Fatalf("TotalItems = %d, want 130", tail.TotalItems)
	}

	record, err := store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !reflect.DeepEqual(tail.Record.Metadata, record.Metadata) {
		t.Fatalf("tail metadata = %#v, want %#v", tail.Record.Metadata, record.Metadata)
	}
	if tail.Record.Title != record.Title || tail.Record.Preview != record.Preview {
		t.Fatalf("tail record = %#v, want %#v", tail.Record, record)
	}

	page, err := store.ListItems(threadID, ListItemsOptions{PageSize: 60, SortDirection: SortDesc})
	if err != nil {
		t.Fatalf("ListItems() error = %v", err)
	}
	if tail.NextCursor != page.NextCursor {
		t.Fatalf("tail cursor = %q, want %q", tail.NextCursor, page.NextCursor)
	}
	want := make([]Item, 0, len(page.Items))
	for index := len(page.Items) - 1; index >= 0; index-- {
		want = append(want, page.Items[index])
	}
	if !reflect.DeepEqual(tail.Items, want) {
		t.Fatalf("tail items = %#v, want %#v", tail.Items, want)
	}
}

// TestReadTailPageFallsBackLikeRust covers the records the bounded read must
// leave to the full store path: inherited history and a stored layout without
// an item array.
func TestReadTailPageFallsBackLikeRust(t *testing.T) {
	store := NewStore(t.TempDir())
	threadID := ThreadID("123e4567-e89b-12d3-a456-426614174344")
	sourceID := ThreadID("123e4567-e89b-12d3-a456-426614174345")
	saveTailTestRecord(t, store, sourceID, tailTestItems(3))
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := store.Save(&Record{
		ID:          threadID,
		SessionID:   string(threadID),
		CreatedAt:   now,
		UpdatedAt:   now,
		RecencyAt:   now,
		HistoryBase: &HistoryPosition{ThreadID: sourceID, ItemEnd: 3, TurnEnd: 0},
		Items:       tailTestItems(2),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, ok, err := store.ReadTailPage(threadID, 60); err != nil || ok {
		t.Fatalf("ReadTailPage() ok=%v err=%v, want a fallback for inherited history", ok, err)
	}

	path, err := store.Path(threadID)
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"id":"`+string(threadID)+`"}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, ok, err := store.ReadTailPage(threadID, 60); err != nil || ok {
		t.Fatalf("ReadTailPage() ok=%v err=%v, want a fallback without an item array", ok, err)
	}
}

// TestReadTailPageShortRecordLikeRust keeps a record below the page size on the
// full path: there is no cursor and the caller renders the whole history.
func TestReadTailPageShortRecordLikeRust(t *testing.T) {
	store := NewStore(t.TempDir())
	threadID := ThreadID("123e4567-e89b-12d3-a456-426614174355")
	saveTailTestRecord(t, store, threadID, tailTestItems(4))

	tail, ok, err := store.ReadTailPage(threadID, 60)
	if err != nil || !ok {
		t.Fatalf("ReadTailPage() ok=%v err=%v", ok, err)
	}
	if tail.TotalItems != 4 || len(tail.Items) != 4 || tail.NextCursor != "" {
		t.Fatalf("tail = %+v, want all four items and no cursor", tail)
	}
}

// TestReadTailPageRejectsUnorderedOrdinalsLikeRust falls back rather than
// returning a page whose order differs from ListItems.
func TestReadTailPageRejectsUnorderedOrdinalsLikeRust(t *testing.T) {
	store := NewStore(t.TempDir())
	threadID := ThreadID("123e4567-e89b-12d3-a456-426614174366")
	items := tailTestItems(6)
	items[5].CreatedAtOrdinal = 1
	saveTailTestRecord(t, store, threadID, items)
	if _, ok, err := store.ReadTailPage(threadID, 3); err != nil || ok {
		t.Fatalf("ReadTailPage() ok=%v err=%v, want a fallback for unordered ordinals", ok, err)
	}
}

var _ = filepath.Join
