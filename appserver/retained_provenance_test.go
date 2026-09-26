package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/session"
)

// Mirrors Rust's ContextManager::record_annotated_items: the captured source and
// the acceptance order are written into the recorded item's harness metadata, so a
// resumed thread restores the same delivery proof instead of minting a new
// revision.
func TestRuntimeRouterAnnotatesRetainedProvenanceLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-retained-provenance")
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID)}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	items := []session.Item{
		{
			ID: "user-1", Type: "message", Role: "user", Text: "Keep the repository private.",
			Metadata: map[string]any{"turnId": "turn-1"},
			// A real instruction carries the harness classifications that prove it
			// is genuine user content.
			Raw: json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"Keep the repository private."}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.text"]}}`),
		},
		{
			ID: "assistant-1", Type: "message", Role: "assistant", Text: "Understood.",
			Metadata: map[string]any{"turnId": "turn-1"},
			Raw:      json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Understood."}]}`),
		},
	}
	if _, err := router.runtimeAppendItems(threadID, items); err != nil {
		t.Fatalf("runtimeAppendItems() error = %v", err)
	}

	record, err := store.Read(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("Read() = %#v/%v", record, err)
	}
	persisted := map[string]retainedctx.HarnessMetadata{}
	for index := range record.Items {
		item := &record.Items[index]
		if _, ok := item.Data[harnessMetadataKey]; !ok {
			t.Fatalf("item %s carries no harness metadata: %#v", item.ID, item.Data)
		}
		metadata := harnessMetadataFromSessionItem(item)
		if metadata == nil {
			t.Fatalf("harness metadata for %s = %#v", item.ID, item.Data[harnessMetadataKey])
		}
		if metadata.RetainedSource == nil || metadata.UserInputOrder == nil {
			t.Fatalf("item %s provenance = %#v", item.ID, metadata)
		}
		if metadata.RetainedSource.ID.MessageID != item.ID || metadata.RetainedSource.ID.TurnID != "turn-1" ||
			!metadata.RetainedSource.Complete || metadata.RetainedSource.Revision == "" {
			t.Fatalf("item %s source = %#v", item.ID, metadata.RetainedSource)
		}
		persisted[item.ID] = *metadata
	}
	if persisted["user-1"].RetainedSource.ID.Role != retainedctx.RetainedSourceRoleUser ||
		persisted["assistant-1"].RetainedSource.ID.Role != retainedctx.RetainedSourceRoleAssistant {
		t.Fatalf("source roles = %#v / %#v",
			persisted["user-1"].RetainedSource.ID.Role, persisted["assistant-1"].RetainedSource.ID.Role)
	}
	// The instruction keeps its accepted position ahead of the reply.
	if *persisted["user-1"].UserInputOrder >= *persisted["assistant-1"].UserInputOrder {
		t.Fatalf("orders = %d / %d, want the instruction first",
			*persisted["user-1"].UserInputOrder, *persisted["assistant-1"].UserInputOrder)
	}

	// A fresh router (a restarted process) restores the same revisions and orders.
	fresh := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	context := fresh.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil after restart")
	}
	restored := map[string]*retainedctx.RetainedSource{}
	orders := map[string]uint64{}
	entries := context.OrderedEntries()
	for _, entry := range entries {
		source := context.Source(entry.Entry)
		if source == nil {
			t.Fatalf("restored entry has no source: %#v", entry)
		}
		restored[source.ID.MessageID] = source
		orders[source.ID.MessageID] = entry.Order.Order
	}
	for id, want := range persisted {
		got := restored[id]
		if got == nil {
			t.Fatalf("restored sources = %#v, want %s", restored, id)
		}
		if got.Revision != want.RetainedSource.Revision || got.Complete != want.RetainedSource.Complete {
			t.Fatalf("%s revision = %v, want the recorded %v", id, got.Revision, want.RetainedSource.Revision)
		}
		if orders[id] != *want.UserInputOrder {
			t.Fatalf("%s order = %d, want the recorded %d", id, orders[id], *want.UserInputOrder)
		}
	}
}

// A recorded item whose source was captured incomplete stays incomplete, so the
// reviewer never treats shortened evidence as complete authorization (Rust's
// `complete &= metadata.retained_source.complete`).
func TestRuntimeRouterKeepsRecordedIncompleteProvenanceLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-retained-incomplete")
	order := uint64(0)
	source := retainedctx.RetainedSource{
		ID: retainedctx.RetainedSourceID{
			MessageID: "user-1",
			TurnID:    "turn-1",
			Role:      retainedctx.RetainedSourceRoleUser,
		},
		Revision: retainedctx.NewResponseItemID("retained"),
		Complete: false,
	}
	raw, err := json.Marshal(map[string]any{
		"retained_source":  source,
		"user_input_order": order,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Items: []session.Item{{
			ID: "user-1", Type: "message", Role: "user", Text: "Keep the repository private.",
			Metadata: map[string]any{"turnId": "turn-1"},
			Data:     map[string]any{harnessMetadataKey: json.RawMessage(raw)},
			// The classifications prove genuine user content, so the recorded
			// source's incompleteness is the only reason this stays incomplete.
			Raw: json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"Keep the repository private."}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.text"]}}`),
		}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	entries := context.OrderedEntries()
	if len(entries) != 1 || entries[0].Entry.UserMessage == nil {
		t.Fatalf("retained entries = %#v", entries)
	}
	if entries[0].Entry.UserMessage.Complete {
		t.Fatal("a recorded incomplete source was retained as complete evidence")
	}
	if got := context.Source(entries[0].Entry); got == nil || got.Revision != source.Revision {
		t.Fatalf("source revision = %#v, want the recorded %v", got, source.Revision)
	}
	if entries[0].Order.Order != order {
		t.Fatalf("order = %d, want the recorded %d", entries[0].Order.Order, order)
	}
}
