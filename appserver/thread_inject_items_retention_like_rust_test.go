package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

func injectRetentionMetadata(t *testing.T, item session.Item) (bool, bool) {
	t.Helper()
	var metadata map[string]any
	switch value := item.Data["harness_metadata"].(type) {
	case json.RawMessage:
		if err := json.Unmarshal(value, &metadata); err != nil {
			t.Fatalf("unmarshal harness metadata: %v", err)
		}
	case map[string]any:
		metadata = value
	case nil:
		return false, false
	default:
		t.Fatalf("unexpected harness metadata type %T", value)
	}
	if _, legacy := metadata["client_authored"]; legacy {
		t.Fatalf("metadata keeps the legacy client_authored marker: %#v", metadata)
	}
	retain, ok := metadata["retain_across_windows"].(bool)
	return retain, ok
}

// Rust #61344: wrapped injections persist `retention: default|always` in
// `retain_across_windows`, raw items keep the thread's configured retention
// rules, and the legacy `client_authored` marker is no longer written.
func TestThreadInjectItemsPersistsExplicitRetentionLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-retention")
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	if err := store.Create(&session.Record{ID: threadID, CreatedAt: now, UpdatedAt: now, RecencyAt: now}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	router := NewRouter(store)
	router.retainClientDeveloperMessages = func() bool { return true }

	items := []json.RawMessage{
		json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"raw"}]}`),
		json.RawMessage(`{"item":{"type":"message","role":"developer","content":[{"type":"input_text","text":"default"}]},"retention":"default"}`),
		json.RawMessage(`{"item":{"type":"message","role":"developer","content":[{"type":"input_text","text":"always"}]},"retention":"always"}`),
		json.RawMessage(`{"item":{"type":"function_call_output","name":"shell","output":"remember"},"retention":"always"}`),
	}
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: string(threadID),
		Items:    items,
	}))
	if response.Error != nil {
		t.Fatalf("inject items error = %+v", response.Error)
	}
	record, err := store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if len(record.Items) != len(items) {
		t.Fatalf("items = %#v", record.Items)
	}
	want := []struct {
		retain   bool
		explicit bool
	}{
		{explicit: false},
		{retain: false, explicit: true},
		{retain: true, explicit: true},
		{retain: true, explicit: true},
	}
	for index := range record.Items {
		retain, explicit := injectRetentionMetadata(t, record.Items[index])
		if retain != want[index].retain || explicit != want[index].explicit {
			t.Fatalf("item %d retention = (%v,%v), want (%v,%v): %#v",
				index, retain, explicit, want[index].retain, want[index].explicit, record.Items[index])
		}
	}
}

// Rust #61344: explicitly retained tool calls and outputs must be adjacent in
// the same request, call first, with both marked always. A retained output
// that carries a call id has the same requirement.
func TestThreadInjectItemsRequiresRetainedToolAdjacencyLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-adjacency")
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	if err := store.Create(&session.Record{ID: threadID, CreatedAt: now, UpdatedAt: now, RecencyAt: now}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	router := NewRouter(store)

	callAlways := json.RawMessage(`{"item":{"type":"function_call","call_id":"call-1","name":"shell","arguments":"{}"},"retention":"always"}`)
	outputAlways := json.RawMessage(`{"item":{"type":"function_call_output","call_id":"call-1","output":"done"},"retention":"always"}`)
	outputDefault := json.RawMessage(`{"item":{"type":"function_call_output","call_id":"call-1","output":"done"},"retention":"default"}`)
	mismatched := json.RawMessage(`{"item":{"type":"function_call_output","call_id":"call-2","output":"done"},"retention":"always"}`)

	accepted := router.Handle(requestWithParams(t, IntID(1), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: string(threadID),
		Items:    []json.RawMessage{callAlways, outputAlways},
	}))
	if accepted.Error != nil {
		t.Fatalf("matching retained exchange rejected: %+v", accepted.Error)
	}

	cases := []struct {
		name  string
		items []json.RawMessage
	}{
		{name: "call without output", items: []json.RawMessage{callAlways}},
		{name: "call with default output", items: []json.RawMessage{callAlways, outputDefault}},
		{name: "mismatched call id", items: []json.RawMessage{callAlways, mismatched}},
		{name: "standalone retained output", items: []json.RawMessage{outputAlways}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := router.Handle(requestWithParams(t, IntID(2), MethodThreadInjectItems, ThreadInjectItemsParams{
				ThreadID: string(threadID),
				Items:    testCase.items,
			}))
			if response.Error == nil || !strings.Contains(response.Error.Message, "adjacent in the same request") {
				t.Fatalf("response error = %+v, want the adjacency rejection", response.Error)
			}
		})
	}

	serverSearch := json.RawMessage(`{"item":{"type":"tool_search_output","call_id":"search-1","execution":"server","status":"completed","tools":[]},"retention":"always"}`)
	serverAccepted := router.Handle(requestWithParams(t, IntID(3), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: string(threadID),
		Items:    []json.RawMessage{serverSearch},
	}))
	if serverAccepted.Error != nil {
		t.Fatalf("server tool search output rejected: %+v", serverAccepted.Error)
	}
}

// Wrapped items require a policy; an unknown policy is rejected with the
// variant list, mirroring the Rust enum deserializer.
func TestThreadInjectItemsRejectsInvalidWrappedRetentionLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-invalid-retention")
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	if err := store.Create(&session.Record{ID: threadID, CreatedAt: now, UpdatedAt: now, RecencyAt: now}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	router := NewRouter(store)

	missing := router.Handle(requestWithParams(t, IntID(1), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: string(threadID),
		Items:    []json.RawMessage{json.RawMessage(`{"item":{"type":"message","role":"user","content":[]}}`)},
	}))
	if missing.Error == nil || !strings.Contains(missing.Error.Message, "missing field `retention`") {
		t.Fatalf("missing retention error = %+v", missing.Error)
	}

	unknown := router.Handle(requestWithParams(t, IntID(2), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: string(threadID),
		Items:    []json.RawMessage{json.RawMessage(`{"item":{"type":"message","role":"user","content":[]},"retention":"sometimes"}`)},
	}))
	if unknown.Error == nil || !strings.Contains(unknown.Error.Message, "expected `default`, `always`, `alwaysInThread` or `alwaysInChildren`") {
		t.Fatalf("unknown retention error = %+v", unknown.Error)
	}

	missingItem := router.Handle(requestWithParams(t, IntID(3), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: string(threadID),
		Items:    []json.RawMessage{json.RawMessage(`{"retention":"always"}`)},
	}))
	if missingItem.Error == nil || !strings.Contains(missingItem.Error.Message, "missing field `item`") {
		t.Fatalf("missing item error = %+v", missingItem.Error)
	}
}

// The active-turn steering mailbox receives the unwrapped response item, not
// the injection wrapper.
func TestThreadInjectItemsRuntimeEnqueuesUnwrappedPayloadLikeRust(t *testing.T) {
	mailbox := turn.NewSteerMailbox()
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, SteerMailbox: mailbox, DefaultCWD: t.TempDir()})
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	started := threadRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if started.Error != nil {
		t.Fatal(started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	router.markResponseThreadLoaded(started.Result, "default")
	if err := router.registerActiveRuntimeTurn(threadID, "turn-inject", func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"item":{"type":"message","role":"developer","content":[{"type":"input_text","text":"wrapped context"}]},"retention":"always"}`)
	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadInjectItems, ThreadInjectItemsParams{ThreadID: threadID, Items: []json.RawMessage{raw}}))
	if response.Error != nil {
		t.Fatalf("response=%#v", response.Error)
	}
	items := mailbox.Drain(&turn.SteerDrainParams{ThreadID: threadID, TurnID: "turn-inject"})
	if len(items) != 1 {
		t.Fatalf("mailbox=%#v", items)
	}
	encoded, _ := json.Marshal(items[0])
	if strings.Contains(string(encoded), `"retention"`) || strings.Contains(string(encoded), `"item":{"type"`) {
		t.Fatalf("mailbox item kept the injection wrapper: %s", encoded)
	}
	if !strings.Contains(string(encoded), "wrapped context") {
		t.Fatalf("mailbox item = %s", encoded)
	}
}
