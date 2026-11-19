package app

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"codex_go/session"
)

// TestInteractiveHistoryWindowFromRecordMatchesStorePageLikeRust pins the fast
// resume path to the store-backed page: Rust's budgeted initial hydration
// (#46739) pages the store once and renders only that window, so cutting the
// page from the record already in memory must return exactly the same
// transcript and cursor.
func TestInteractiveHistoryWindowFromRecordMatchesStorePageLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("123e4567-e89b-12d3-a456-426614174222")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	items := make([]session.Item, 0, 130)
	for index := 0; index < 130; index++ {
		role := "assistant"
		text := fmt.Sprintf("answer-%03d", index)
		if index%10 == 0 {
			role = "user"
			text = fmt.Sprintf("prompt-%03d", index)
		}
		item := session.Item{
			ID:        fmt.Sprintf("item-%03d", index),
			Type:      "message",
			Role:      role,
			Text:      text,
			CreatedAt: now.Add(time.Duration(index) * time.Second),
		}
		if index%7 != 0 {
			item.CreatedAtOrdinal = uint64(index + 1)
		}
		items = append(items, item)
	}
	if err := store.Save(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Title:     "Long fast-path thread",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: `D:\repo`},
		Items:     items,
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	record, err := store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}

	wantMessages, wantCursor, wantOlder, err := interactiveSessionHistoryWindow(store, threadID, record.Metadata.CWD)
	if err != nil {
		t.Fatalf("store history window returned error: %v", err)
	}
	gotMessages, gotCursor, gotOlder, ok := interactiveSessionHistoryWindowFromRecord(record)
	if !ok {
		t.Fatal("windowFromRecord refused a long record")
	}
	if gotCursor != wantCursor || gotOlder != wantOlder {
		t.Fatalf("windowFromRecord cursor=%q older=%v, want cursor=%q older=%v", gotCursor, gotOlder, wantCursor, wantOlder)
	}
	if !reflect.DeepEqual(gotMessages, wantMessages) {
		t.Fatalf("windowFromRecord messages = %#v, want %#v", gotMessages, wantMessages)
	}
	if len(gotMessages) != interactiveResumeInitialItemBudget {
		t.Fatalf("windowFromRecord messages = %d, want %d", len(gotMessages), interactiveResumeInitialItemBudget)
	}
	if got := gotMessages[len(gotMessages)-1].Text; got != "answer-129" {
		t.Fatalf("newest window message = %q, want answer-129", got)
	}
}

// TestInteractiveHistoryWindowFromRecordSkipsShortRecordsLikeRust keeps the
// short-thread resume path on the full projection: below the budget Rust
// renders the whole loaded history without a paging cursor.
func TestInteractiveHistoryWindowFromRecordSkipsShortRecordsLikeRust(t *testing.T) {
	record := &session.Record{Items: []session.Item{{ID: "item-1", Type: "message", Role: "user", Text: "hi"}}}
	if window, cursor, older, ok := interactiveSessionHistoryWindowFromRecord(record); ok || window != nil || cursor != "" || older {
		t.Fatalf("windowFromRecord short record = %#v %q %v %v, want no window", window, cursor, older, ok)
	}
	if window, _, _, ok := interactiveSessionHistoryWindowFromRecord(nil); ok || window != nil {
		t.Fatalf("windowFromRecord(nil) = %#v %v, want no window", window, ok)
	}
}
