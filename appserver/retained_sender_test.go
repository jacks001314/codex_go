package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/session"
	"codex_go/state"
)

// Mirrors Rust's `LocalAgentRuntime::capture_sender_user_messages`
// (guardian_sender_messages_tests.rs): a trusted delegation delivery captures up
// to three recent complete local user messages from the sender thread, a
// recognized delivery without provenance still gets its own snapshot, and the
// evidence reaches the receiver's retained context.
func TestRuntimeRouterCapturesSenderUserMessagesLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	sender := session.ThreadID("thread-sender")
	receiverApp := session.ThreadID("thread-receiver-app")
	receiverTUI := session.ThreadID("thread-receiver-tui")
	for _, id := range []session.ThreadID{sender, receiverApp, receiverTUI} {
		if err := store.Create(&session.Record{ID: id, SessionID: string(id)}); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})

	prompts := []string{
		"OLDEST",
		"Inspect the experiment.",
		"Add twenty workers.",
		"Only use staging.\nNever production.",
	}
	var items []session.Item
	for index, prompt := range prompts {
		raw, err := json.Marshal(map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": prompt}},
			"internal_chat_message_metadata_passthrough": map[string]any{"content_item_kinds": []string{"user.text"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, session.Item{
			ID:       fmt.Sprintf("sender-user-%d", index),
			Type:     "message",
			Role:     "user",
			Text:     prompt,
			Metadata: map[string]any{"turnId": fmt.Sprintf("sender-turn-%d", index)},
			Raw:      raw,
		})
	}
	if _, err := router.runtimeAppendItems(sender, items); err != nil {
		t.Fatalf("runtimeAppendItems(sender) error = %v", err)
	}

	provenance := fmt.Sprintf("<codex_delegation>\n  <source_thread_id>%s</source_thread_id>\n  <input>Inspect.</input>\n</codex_delegation>", sender)
	for _, testCase := range []struct {
		name      string
		receiver  session.ThreadID
		namespace string
		output    string
		wantLines []string
		wantSourc string
	}{
		{
			name:      "trusted delegation captures sender instructions",
			receiver:  receiverApp,
			namespace: "codex_app",
			output:    provenance,
			// The oldest instruction is dropped and the multi-line message keeps
			// one role label per line.
			wantLines: []string{"user: Inspect the experiment.", "user: Add twenty workers.", "user: Only use staging.", "user: Never production."},
			wantSourc: "Source thread: " + string(sender) + "\n",
		},
		{
			name:      "recognized delivery without provenance",
			receiver:  receiverTUI,
			namespace: "codex_tui",
			output:    "Inspect again without sender provenance.",
			wantLines: nil,
			wantSourc: "Source thread: unavailable\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			delivery := session.Item{
				ID:        "delivery-" + testCase.namespace,
				Type:      "function_call_output",
				Name:      "send_message_to_thread",
				Namespace: testCase.namespace,
				Text:      testCase.output,
			}
			if _, err := router.runtimeAppendItems(testCase.receiver, []session.Item{delivery}); err != nil {
				t.Fatalf("runtimeAppendItems(receiver) error = %v", err)
			}

			record, err := store.Read(testCase.receiver, true, true)
			if err != nil || record == nil {
				t.Fatalf("Read() = %#v/%v", record, err)
			}
			var persisted *retainedctx.HarnessMetadata
			for index := range record.Items {
				item := &record.Items[index]
				if item.ID != delivery.ID {
					continue
				}
				if persisted = harnessMetadataFromSessionItem(item); persisted == nil {
					t.Fatalf("delivery %s carries no harness metadata", item.ID)
				}
			}
			if persisted == nil || persisted.SenderUserMessages == nil {
				t.Fatalf("delivery snapshot = %#v", persisted)
			}
			snapshot := persisted.SenderUserMessages
			if snapshot.ReceiverMessageID != delivery.ID {
				t.Fatalf("receiver message id = %q, want %q", snapshot.ReceiverMessageID, delivery.ID)
			}
			if !strings.Contains(snapshot.Text, testCase.wantSourc) {
				t.Fatalf("snapshot text =\n%s\nwant %q", snapshot.Text, testCase.wantSourc)
			}
			var lines []string
			for _, line := range strings.Split(snapshot.Text, "\n") {
				if strings.HasPrefix(line, "user: ") {
					lines = append(lines, line)
				}
			}
			if strings.Join(lines, "|") != strings.Join(testCase.wantLines, "|") {
				t.Fatalf("sender lines = %#v, want %#v", lines, testCase.wantLines)
			}
			// The markers wrap the whole fragment, matching Rust's
			// ContextualUserFragment rendering.
			if !strings.HasPrefix(snapshot.Text, ">>> SENDER USER MESSAGES START\n") ||
				!strings.HasSuffix(snapshot.Text, ">>> SENDER USER MESSAGES END\n") {
				t.Fatalf("snapshot markers = %q", snapshot.Text)
			}

			router.retainedContextsMu.Lock()
			context := router.retainedLiveContextLocked(string(testCase.receiver))
			router.retainedContextsMu.Unlock()
			if context == nil || context.SenderUserMessages() == nil {
				t.Fatalf("receiver retained context holds no sender delivery: %#v", context)
			}
			if context.SenderUserMessages().Text != snapshot.Text {
				t.Fatalf("retained sender text =\n%s\nwant\n%s", context.SenderUserMessages().Text, snapshot.Text)
			}
			// The reviewer-only section contributes exactly the captured text.
			if section := state.SenderUserMessagesSectionItems(context); len(section) != 1 || section[0] != snapshot.Text {
				t.Fatalf("sender section = %#v, want the captured snapshot", section)
			}
		})
	}
}

// A paired tool result and an unregistered tool name never capture, matching
// Rust's destructuring (`call_id: None`, name/namespace match).
func TestRuntimeRouterSenderCaptureGatesLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	if err := store.Create(&session.Record{ID: "thread-x", SessionID: "thread-x"}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	for _, item := range []session.Item{
		{ID: "paired", Type: "function_call_output", CallID: "call-1", Name: "send_message_to_thread", Namespace: "codex_app", Text: "ok"},
		{ID: "other-name", Type: "function_call_output", Name: "create_thread", Namespace: "codex_app", Text: "ok"},
		{ID: "other-namespace", Type: "function_call_output", Name: "send_message_to_thread", Namespace: "untrusted", Text: "ok"},
	} {
		if _, ok := router.captureSenderUserMessages(&item, "thread-x", "turn-x"); ok {
			t.Fatalf("item %s should not capture sender context", item.ID)
		}
	}
}
