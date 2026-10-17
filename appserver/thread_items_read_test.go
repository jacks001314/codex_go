package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// TestRouterThreadItemsReadByIDLikeRust mirrors Rust #52637: thread/items/read
// returns only requested ids from the requested turn, in canonical stored
// order, omitting missing ids, and rejects requests outside 1..100 ids.
func TestRouterThreadItemsReadByIDLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	item := func(id string, turnID string, text string) session.Item {
		return session.Item{
			ID:        id,
			Type:      "message",
			Role:      "user",
			Text:      text,
			CreatedAt: now,
			Data:      map[string]any{"turn_id": turnID},
		}
	}
	record := &session.Record{
		ID:        "thread-1",
		SessionID: "thread-1",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: t.TempDir(), Model: "gpt-5", ModelProvider: "openai"},
		Items: []session.Item{
			item("item-1", "turn-1", "first"),
			item("item-2", "turn-2", "other turn"),
			item("item-3", "turn-1", "third"),
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRouter(store)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadItemsRead, ThreadItemsReadParams{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemIDs:  []string{"item-3", "item-1", "missing"},
	}))
	if response.Error != nil {
		t.Fatalf("thread/items/read error = %+v", response.Error)
	}
	read := response.Result.(*ThreadItemsReadResponse)
	if len(read.Data) != 2 || read.Data[0].Item.ID != "item-1" || read.Data[1].Item.ID != "item-3" {
		t.Fatalf("thread/items/read data = %#v", read.Data)
	}

	tooMany := make([]string, MaxThreadItemsReadIDs+1)
	for index := range tooMany {
		tooMany[index] = "item-" + strings.Repeat("x", index)
	}
	for _, testCase := range []struct {
		name   string
		params ThreadItemsReadParams
		want   string
	}{
		{name: "no ids", params: ThreadItemsReadParams{ThreadID: "thread-1", TurnID: "turn-1"}, want: "at least one item id"},
		{name: "too many ids", params: ThreadItemsReadParams{ThreadID: "thread-1", TurnID: "turn-1", ItemIDs: tooMany}, want: "at most 100 item ids"},
		{name: "missing turn", params: ThreadItemsReadParams{ThreadID: "thread-1", ItemIDs: []string{"item-1"}}, want: "turnId is required"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			bad := router.Handle(requestWithParams(t, IntID(2), MethodThreadItemsRead, testCase.params))
			if bad.Error == nil || bad.Error.Code != -32600 || !strings.Contains(bad.Error.Message, testCase.want) {
				t.Fatalf("error = %+v, want %q", bad.Error, testCase.want)
			}
		})
	}
}
