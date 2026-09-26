package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/session"
)

// Mirrors Rust spawn.rs's forked-item provenance: every copied conversational
// message is marked inherited, the parent's sender evidence is dropped, and an
// item whose position belongs to the parent's counter loses the parent's
// acceptance order.
func TestMarkInheritedUserMessagesLikeRust(t *testing.T) {
	userOrder := uint64(3)
	items := []session.Item{
		func() session.Item {
			item := sessionItemForRetainedTest("user-1", "Keep the repository private.", []string{"user.text"}, []string{"input_text"})
			raw, err := json.Marshal(map[string]any{
				"inherited_user_message": false,
				"user_input_order":       userOrder,
			})
			if err != nil {
				t.Fatal(err)
			}
			item.Data = map[string]any{harnessMetadataKey: json.RawMessage(raw)}
			return item
		}(),
		{
			ID: "assistant-1", Type: "message", Role: "assistant", Text: "Understood.",
			Data: map[string]any{harnessMetadataKey: json.RawMessage(`{"user_input_order":4}`)},
		},
		{
			ID: "tool-1", Type: "function_call_output", CallID: "call-1",
			Data: map[string]any{harnessMetadataKey: json.RawMessage(`{"user_input_order":5}`)},
		},
	}
	markInheritedUserMessages(items)

	userMetadata := harnessMetadataFromSessionItem(&items[0])
	if userMetadata == nil || !userMetadata.InheritedUserMessage {
		t.Fatalf("inherited user message metadata = %#v", userMetadata)
	}
	if userMetadata.UserInputOrder == nil || *userMetadata.UserInputOrder != userOrder {
		t.Fatalf("a copied user instruction lost its position: %#v", userMetadata)
	}
	assistantMetadata := harnessMetadataFromSessionItem(&items[1])
	if assistantMetadata == nil || !assistantMetadata.InheritedUserMessage || assistantMetadata.UserInputOrder != nil {
		t.Fatalf("assistant provenance = %#v", assistantMetadata)
	}
	// A non-message item keeps its own metadata untouched.
	if raw := harnessMetadataRawFromItem(&items[2]); string(raw) != `{"user_input_order":5}` {
		t.Fatalf("tool metadata = %s", raw)
	}

	// A copied delivery drops the parent's sender evidence.
	delivery := sessionItemForRetainedTest("user-2", "Delegated.", []string{"user.text"}, []string{"input_text"})
	delivery.Data = map[string]any{harnessMetadataKey: json.RawMessage(
		`{"sender_user_messages":{"receiver_turn_id":"turn-parent","receiver_message_id":"msg-1","text":"Sender: root\n"},"user_input_order":6}`)}
	markInheritedUserMessages([]session.Item{delivery})
	deliveryMetadata := harnessMetadataFromSessionItem(&delivery)
	if deliveryMetadata == nil || !deliveryMetadata.InheritedUserMessage {
		t.Fatalf("delivery provenance = %#v", deliveryMetadata)
	}
	if deliveryMetadata.SenderUserMessages != nil || deliveryMetadata.UserInputOrder != nil {
		t.Fatalf("a copied delivery kept the parent's evidence: %#v", deliveryMetadata)
	}
}

// Mirrors Rust's `retain_inherited_user_messages`: a worker keeps the
// root-authorization section instead of presenting the parent's adopted
// instructions as its own evidence, while a root thread adopts the copied prefix
// with the inherited scope.
func TestRuntimeRouterKeepsInheritedInstructionsOutOfAWorkerLikeRust(t *testing.T) {
	inheritedItem := func(id string) session.Item {
		item := sessionItemForRetainedTest(id, "Keep the repository private.", []string{"user.text"}, []string{"input_text"})
		raw, err := json.Marshal(map[string]any{"inherited_user_message": true})
		if err != nil {
			t.Fatal(err)
		}
		item.Data = map[string]any{harnessMetadataKey: json.RawMessage(raw)}
		return item
	}
	for _, testCase := range []struct {
		name      string
		subagent  bool
		wantEntry bool
	}{
		{name: "root thread adopts the copied prefix", wantEntry: true},
		{name: "worker omits the copied prefix", subagent: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			store := session.NewStore(home)
			threadID := session.ThreadID("thread-inherited")
			record := &session.Record{ID: threadID, SessionID: string(threadID), Items: []session.Item{inheritedItem("user-1")}}
			if testCase.subagent {
				record.Metadata.Originator = "subagent"
				record.Metadata.ThreadSource = "subAgentThreadSpawn"
			}
			if err := store.Create(record); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{
				ThreadRouter: NewRouter(store),
				Config:       config.NewConfigService(home),
			})
			context := router.retainedContextForThread(string(threadID))
			if !testCase.wantEntry {
				if context != nil {
					t.Fatalf("worker retained evidence = %#v, want none", context.OrderedEntries())
				}
				return
			}
			if context == nil {
				t.Fatal("root thread retained no evidence")
			}
			entries := context.OrderedEntries()
			if len(entries) != 1 || entries[0].Entry.UserMessage == nil {
				t.Fatalf("root retained entries = %#v", entries)
			}
			if !entries[0].Order.Inherited {
				t.Fatalf("adopted instruction scope = %#v, want the inherited prefix", entries[0].Order)
			}
			source := context.Source(entries[0].Entry)
			if source == nil || source.ID.Role != retainedctx.RetainedSourceRoleUser {
				t.Fatalf("adopted instruction source = %#v", source)
			}
		})
	}
}

// A worker that also holds its own local instruction keeps that evidence, so
// dropping the reviewer's subagent gate cannot hide the worker's own input.
func TestRuntimeRouterKeepsAWorkersOwnInstructionsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-worker-local")
	inherited := sessionItemForRetainedTest("user-inherited", "Parent instruction.", []string{"user.text"}, []string{"input_text"})
	inheritedRaw, err := json.Marshal(map[string]any{"inherited_user_message": true})
	if err != nil {
		t.Fatal(err)
	}
	inherited.Data = map[string]any{harnessMetadataKey: json.RawMessage(inheritedRaw)}
	local := sessionItemForRetainedTest("user-local", "Worker instruction.", []string{"user.text"}, []string{"input_text"})
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID),
		Items:    []session.Item{inherited, local},
		Metadata: session.Metadata{Originator: "subagent", ThreadSource: "subAgentThreadSpawn"},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	context := router.guardianRetainedContextForTurn(string(threadID), "turn-1")
	if context == nil {
		t.Fatal("guardianRetainedContextForTurn() = nil for a worker's own instruction")
	}
	entries := context.OrderedEntries()
	if len(entries) != 1 || entries[0].Entry.UserMessage == nil || entries[0].Entry.UserMessage.Text != "Worker instruction." {
		t.Fatalf("worker retained entries = %#v", entries)
	}
	if entries[0].Order.Inherited {
		t.Fatalf("worker's own instruction scope = %#v, want local", entries[0].Order)
	}
}
