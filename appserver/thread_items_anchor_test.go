package appserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
)

// createAnchorRecord builds a legacy record with a three-item turn and a second
// turn, so an item anchor can page inside one turn.
func createAnchorRecord(t *testing.T, store *session.Store, id session.ThreadID, now time.Time) {
	t.Helper()
	err := store.Create(&session.Record{
		ID: id, SessionID: string(id), Title: "title", Preview: "preview",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: "D:/repo", ModelProvider: "openai", Source: "cli", HistoryMode: string(ThreadHistoryLegacy)},
		Items: []session.Item{
			{ID: "anchor-a", Type: "message", Role: "user", Text: "a", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "anchor-b", Type: "reasoning", Text: "b", CreatedAt: now.Add(time.Second), Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "anchor-c", Type: "message", Role: "assistant", Text: "c", CreatedAt: now.Add(2 * time.Second), Metadata: map[string]any{"turnId": "turn-1"}},
			{ID: "anchor-d", Type: "message", Role: "user", Text: "d", CreatedAt: now.Add(3 * time.Second), Metadata: map[string]any{"turnId": "turn-2"}},
		},
	})
	if err != nil {
		t.Fatalf("create anchor record: %v", err)
	}
}

// Mirrors Rust's ThreadItemsListCursor (#48151): the wire `cursor` is either the
// opaque continuation string or an exclusive item anchor.
func TestThreadItemsListCursorDecodesOpaqueStringAndAnchorLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		raw        string
		wantOpaque string
		wantType   string
		wantItemID string
	}{
		{name: "opaque", raw: `"opaque-cursor"`, wantOpaque: "opaque-cursor"},
		{name: "anchor", raw: `{"type":"item","itemId":"item-123"}`, wantType: "item", wantItemID: "item-123"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var params ThreadItemsListParams
			if err := json.Unmarshal([]byte(`{"threadId":"thread-1","cursor":`+testCase.raw+`}`), &params); err != nil {
				t.Fatalf("unmarshal params: %v", err)
			}
			if params.Cursor == nil {
				t.Fatal("cursor did not decode")
			}
			if params.Cursor.Opaque != testCase.wantOpaque {
				t.Fatalf("opaque cursor = %q", params.Cursor.Opaque)
			}
			if testCase.wantType == "" {
				if params.Cursor.Anchor != nil {
					t.Fatalf("unexpected anchor = %#v", params.Cursor.Anchor)
				}
			} else if params.Cursor.Anchor == nil || params.Cursor.Anchor.Type != testCase.wantType || params.Cursor.Anchor.ItemID != testCase.wantItemID {
				t.Fatalf("anchor = %#v", params.Cursor.Anchor)
			}

			// The decoded cursor serializes back to the same wire shape, so a
			// client can replay it.
			encoded, err := json.Marshal(params.Cursor)
			if err != nil {
				t.Fatalf("marshal cursor: %v", err)
			}
			if string(encoded) != testCase.raw {
				t.Fatalf("cursor round trip = %s, want %s", encoded, testCase.raw)
			}
		})
	}

	var nullParams ThreadItemsListParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-1","cursor":null}`), &nullParams); err != nil {
		t.Fatalf("null cursor error = %v", err)
	}
	// Rust's `Option<ThreadItemsListCursor>` decodes null to None, which Go
	// represents as a nil pointer.
	if nullParams.Cursor != nil {
		t.Fatalf("null cursor = %#v", nullParams.Cursor)
	}
}

// Mirrors Rust's thread processor: an anchor needs a non-empty turn id, an
// unknown anchor variant is rejected, an anchor must name an item in the
// requested turn, and both failures are invalid params (-32602).
func TestThreadItemsListAnchorValidationAndPagingLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	createAnchorRecord(t, store, "thread-anchor-rpc", fixedTime())

	turnID := "turn-1"
	otherTurn := "turn-2"
	anchor := func(itemID string) *ThreadItemsListCursor {
		return &ThreadItemsListCursor{Anchor: &ThreadItemsListAnchor{Type: "item", ItemID: itemID}}
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "thread-anchor-rpc", Cursor: anchor("anchor-a"),
	}))
	if response.Error == nil || response.Error.Code != -32602 || response.Error.Message != "turnId is required when cursor is an item anchor" {
		t.Fatalf("missing turn id response = %+v", response.Error)
	}

	response = router.Handle(requestWithParams(t, IntID(2), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "thread-anchor-rpc", TurnID: &turnID,
		Cursor: &ThreadItemsListCursor{Anchor: &ThreadItemsListAnchor{Type: "turn", ItemID: "anchor-a"}},
	}))
	if response.Error == nil || response.Error.Code != -32602 || response.Error.Message != "cursor: unknown variant `turn`, expected `item`" {
		t.Fatalf("unknown variant response = %+v", response.Error)
	}

	// Ascending paging returns the items after the anchor, descending the ones
	// before it, and the anchor itself is never part of the page.
	ascending := router.Handle(requestWithParams(t, IntID(3), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "thread-anchor-rpc", TurnID: &turnID, Cursor: anchor("anchor-a"), SortDirection: SortAsc,
	}))
	if ascending.Error != nil {
		t.Fatalf("ascending anchor response = %+v", ascending.Error)
	}
	page := ascending.Result.(*ThreadItemsListResponse)
	if len(page.Data) != 2 || page.Data[0].Item.ID != "anchor-b" || page.Data[1].Item.ID != "anchor-c" {
		t.Fatalf("ascending anchor page = %#v", page)
	}

	descending := router.Handle(requestWithParams(t, IntID(4), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "thread-anchor-rpc", TurnID: &turnID, Cursor: anchor("anchor-c"), SortDirection: SortDesc,
	}))
	if descending.Error != nil {
		t.Fatalf("descending anchor response = %+v", descending.Error)
	}
	if page := descending.Result.(*ThreadItemsListResponse); len(page.Data) != 2 || page.Data[0].Item.ID != "anchor-b" || page.Data[1].Item.ID != "anchor-a" {
		t.Fatalf("descending anchor page = %#v", page)
	}

	// An anchor in another turn is outside the requested history scope.
	response = router.Handle(requestWithParams(t, IntID(5), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "thread-anchor-rpc", TurnID: &otherTurn, Cursor: anchor("anchor-a"),
	}))
	if response.Error == nil || response.Error.Code != -32602 ||
		response.Error.Message != "cursor.itemId does not identify an item in the requested history scope" {
		t.Fatalf("out-of-scope anchor response = %+v", response.Error)
	}
}

// The paginated store path resolves the anchor through SQLite and reports an
// unresolvable anchor as invalid params, mirroring Rust's error mapping.
func TestThreadItemsListAnchorResolvesThroughPaginatedStore(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	store := session.NewStore(home)
	router := NewRouter(store)
	t.Cleanup(func() { _ = router.Close() })
	router.SetStateRuntime(runtime)

	now := fixedTime()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, SessionID: "anchor-history", ThreadID: "anchor-history", Source: "cli", CWD: home,
		ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatal(err)
	}
	for index, item := range []map[string]any{
		{"type": "userMessage", "id": "anchor-user", "content": []map[string]any{{"type": "text", "text": "hello"}}},
		{"type": "reasoning", "id": "anchor-reasoning", "summary": []string{"thinking"}, "content": []string{}},
		{"type": "agentMessage", "id": "anchor-agent", "text": "done", "phase": "final_answer"},
	} {
		payload, marshalErr := json.Marshal(map[string]any{
			"type": "item_completed", "thread_id": "anchor-history", "turn_id": "turn-1",
			"started_at_ms":   now.Add(time.Duration(index) * time.Second).UnixMilli(),
			"completed_at_ms": now.Add(time.Duration(index+1) * time.Second).UnixMilli(), "item": item,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(time.Duration(index+1) * time.Second).Format(time.RFC3339Nano), Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.AppendTurnComplete("turn-1", now.Add(5*time.Second), 5000); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	turnID := "turn-1"
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "anchor-history", TurnID: &turnID,
		Cursor: &ThreadItemsListCursor{Anchor: &ThreadItemsListAnchor{Type: "item", ItemID: "anchor-reasoning"}},
	}))
	if response.Error != nil {
		t.Fatalf("anchored paginated response = %+v", response.Error)
	}
	page := response.Result.(*ThreadItemsListResponse)
	if len(page.Data) != 1 || page.Data[0].Item.ID != "anchor-agent" {
		t.Fatalf("anchored paginated page = %#v", page)
	}

	response = router.Handle(requestWithParams(t, IntID(2), MethodThreadItemsList, ThreadItemsListParams{
		ThreadID: "anchor-history", TurnID: &turnID,
		Cursor: &ThreadItemsListCursor{Anchor: &ThreadItemsListAnchor{Type: "item", ItemID: "missing"}},
	}))
	if response.Error == nil || response.Error.Code != -32602 ||
		response.Error.Message != "cursor.itemId does not identify an item in the requested history scope" {
		t.Fatalf("unresolvable anchor response = %+v", response.Error)
	}
}
