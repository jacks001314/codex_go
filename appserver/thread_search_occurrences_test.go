package appserver

import (
	"testing"
	"time"

	"codex_go/session"
)

func TestThreadSearchOccurrencesPaginatesVisibleMessagesWithUTF16Ranges(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &session.Record{ID: "thread-1", CreatedAt: now, UpdatedAt: now, Items: []session.Item{
		{ID: "user-1", Type: "user_message", Role: "user", Text: "😀 Needle one", CreatedAt: now, Data: map[string]any{"turn_id": "turn-1"}},
		{ID: "commentary", Type: "assistant_message", Role: "assistant", Text: "needle hidden", CreatedAt: now, Data: map[string]any{"turn_id": "turn-1", "phase": "commentary"}},
		{ID: "final-1", Type: "assistant_message", Role: "assistant", Text: "Needle two", CreatedAt: now, Data: map[string]any{"turn_id": "turn-1", "phase": "final_answer"}},
	}}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(store)
	limit := uint32(1)
	first := router.Handle(requestWithParams(t, IntID(1), MethodThreadSearchOccurrences, ThreadSearchOccurrencesParams{ThreadID: "thread-1", SearchTerm: "needle", Limit: &limit}))
	if first.Error != nil {
		t.Fatalf("first error = %#v", first.Error)
	}
	page := first.Result.(*ThreadSearchOccurrencesResponse)
	if len(page.Data) != 1 || page.NextCursor == nil || page.Data[0].SnippetMatchRange.Start != 3 || page.Data[0].SnippetMatchRange.End != 9 {
		t.Fatalf("first page = %#v", page)
	}
	second := router.Handle(requestWithParams(t, IntID(2), MethodThreadSearchOccurrences, ThreadSearchOccurrencesParams{ThreadID: "thread-1", SearchTerm: "needle", Cursor: page.NextCursor, Limit: &limit}))
	if second.Error != nil {
		t.Fatalf("second error = %#v", second.Error)
	}
	next := second.Result.(*ThreadSearchOccurrencesResponse)
	if len(next.Data) != 1 || next.Data[0].ItemID != "final-1" || next.NextCursor != nil {
		t.Fatalf("second page = %#v", next)
	}
}
