package session

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Rust #62314: a prepared fork that references its ancestor's stored history
// keeps that conversation as context, but the ancestor's messages and approvals
// are not the child's authorization evidence. Reading the child projects the
// inherited prefix as inherited context and drops the ancestor's acceptance
// order and captured sender snapshots, while the child's own messages keep
// theirs.
func TestReferencedAgentForkExcludesAncestorAuthorizationLikeRust(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	parentItems := []Item{
		{ID: "parent-user", Type: "message", Role: "user", Text: "approve the deploy",
			Data: map[string]any{"harness_metadata": json.RawMessage(`{"user_input_order":1,"sender_user_messages":{"receiver_turn_id":"turn-parent"}}`)}},
		{ID: "parent-call", Type: "function_call", Role: "assistant", Name: "shell",
			Data: map[string]any{"harness_metadata": json.RawMessage(`{"user_input_order":2}`)}},
		{ID: "parent-assistant", Type: "message", Role: "assistant", Text: "approved",
			Data: map[string]any{"harness_metadata": json.RawMessage(`{"user_input_order":3}`)}},
	}
	parent := &Record{
		ID: "parent", SessionID: "session-root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: Metadata{CWD: t.TempDir(), Model: "gpt-5.4"},
		Items:    parentItems,
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	child := &Record{
		ID: "child-ref", SessionID: "session-root", ForkedFromID: "parent",
		HistoryBase: &HistoryPosition{ThreadID: "parent", ItemEnd: len(parentItems)},
		CreatedAt:   now, UpdatedAt: now, RecencyAt: now,
		Metadata: Metadata{CWD: t.TempDir(), Model: "gpt-5.4", Originator: "subagent"},
		Items: []Item{
			{ID: "child-user", Type: "message", Role: "user", Text: "just the docs",
				Data: map[string]any{"harness_metadata": json.RawMessage(`{"user_input_order":9}`)}},
		},
	}
	if err := store.Create(child); err != nil {
		t.Fatalf("Create(child) error = %v", err)
	}

	materialized, err := store.Read("child-ref", false, true)
	if err != nil {
		t.Fatalf("Read(child) error = %v", err)
	}
	if materialized.InheritedItems != len(parentItems) || len(materialized.Items) != len(parentItems)+1 {
		t.Fatalf("materialized items = %d (inherited %d), want the ancestor prefix plus one local item",
			len(materialized.Items), materialized.InheritedItems)
	}
	for _, id := range []string{"parent-user", "parent-call", "parent-assistant"} {
		item := forkItemByID(t, materialized.Items, id)
		metadata := ItemHarnessMetadata(&item)
		if metadata["inherited_user_message"] != true {
			t.Fatalf("%s harness metadata = %#v, want inherited_user_message", id, metadata)
		}
		if _, ok := metadata["user_input_order"]; ok {
			t.Fatalf("%s kept an ancestor acceptance order: %#v", id, metadata)
		}
		if _, ok := metadata["sender_user_messages"]; ok {
			t.Fatalf("%s kept an ancestor sender snapshot: %#v", id, metadata)
		}
	}
	local := forkItemByID(t, materialized.Items, "child-user")
	localMetadata := ItemHarnessMetadata(&local)
	if localMetadata["inherited_user_message"] == true {
		t.Fatalf("child-local message marked inherited: %#v", localMetadata)
	}
	if order, ok := localMetadata["user_input_order"].(float64); !ok || order != 9 {
		t.Fatalf("child-local acceptance order = %#v, want 9", localMetadata["user_input_order"])
	}

	// Reading the ancestor keeps its own authorization untouched.
	ancestor, err := store.Read("parent", false, true)
	if err != nil {
		t.Fatalf("Read(parent) error = %v", err)
	}
	parentUser := forkItemByID(t, ancestor.Items, "parent-user")
	parentMetadata := ItemHarnessMetadata(&parentUser)
	if parentMetadata["inherited_user_message"] == true {
		t.Fatal("ancestor message marked inherited by the child's read")
	}
	if _, ok := parentMetadata["sender_user_messages"]; !ok {
		t.Fatal("ancestor lost its own sender snapshot")
	}

	// The projection is read-only: the child's stored file still holds only the
	// child's own items.
	path, err := store.Path("child-ref")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Record
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Items) != 1 || persisted.Items[0].ID != "child-user" {
		t.Fatalf("persisted child items = %#v, want only the child's own item", persisted.Items)
	}
}

// A fork that copied its history has its own session id and was marked at spawn
// time, so the read path leaves its items alone.
func TestCopiedAgentForkKeepsItsOwnAuthorizationLikeRust(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &Record{
		ID: "child-copy", SessionID: "child-copy", ForkedFromID: "parent",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: Metadata{CWD: t.TempDir(), Model: "gpt-5.4"},
		Items: []Item{
			{ID: "copied-user", Type: "message", Role: "user", Text: "approve the deploy",
				Data: map[string]any{"harness_metadata": json.RawMessage(`{"user_input_order":1}`)}},
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	materialized, err := store.Read("child-copy", false, true)
	if err != nil {
		t.Fatal(err)
	}
	item := forkItemByID(t, materialized.Items, "copied-user")
	metadata := ItemHarnessMetadata(&item)
	if metadata["inherited_user_message"] == true {
		t.Fatalf("copied fork item marked inherited: %#v", metadata)
	}
	if order, ok := metadata["user_input_order"].(float64); !ok || order != 1 {
		t.Fatalf("copied fork order = %#v, want 1", metadata["user_input_order"])
	}
}

func forkItemByID(t *testing.T, items []Item, id string) Item {
	t.Helper()
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("item %q missing from %#v", id, items)
	return Item{}
}
