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
// to three recent sender exchanges — each user message with the assistant
// context recorded before it — a recognized delivery without provenance still
// gets its own snapshot, and the evidence reaches the receiver's retained
// context. Rust's #49951 also accepts the cloud-thread producer's compact
// envelope and lets a nested assistant notice survive as untrusted context.
func TestRuntimeRouterCapturesSenderUserMessagesLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	sender := session.ThreadID("thread-sender")
	receiverApp := session.ThreadID("thread-receiver-app")
	receiverTUI := session.ThreadID("thread-receiver-tui")
	receiverCloud := session.ThreadID("thread-receiver-cloud")
	for _, id := range []session.ThreadID{sender, receiverApp, receiverTUI, receiverCloud} {
		if err := store.Create(&session.Record{ID: id, SessionID: string(id)}); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})

	// The sender thread records alternating user instructions and assistant
	// replies, so every delivery can carry the preceding assistant context.
	turns := []struct {
		user      string
		assistant string
	}{
		{user: "OLDEST", assistant: "I can inspect the experiment."},
		{user: "Inspect the experiment.", assistant: "Rerun only the staging task?\nPreserve its checkpoints?"},
		{user: "Add twenty workers.", assistant: "I will use staging."},
		{user: "Only use staging.\nNever production.", assistant: "LATER CONTEXT"},
	}
	var items []session.Item
	for index, turn := range turns {
		raw, err := json.Marshal(map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": turn.user}},
			"internal_chat_message_metadata_passthrough": map[string]any{"content_item_kinds": []string{"user.text"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, session.Item{
			ID:       fmt.Sprintf("sender-user-%d", index),
			Type:     "message",
			Role:     "user",
			Text:     turn.user,
			Metadata: map[string]any{"turnId": fmt.Sprintf("sender-turn-%d", index)},
			Raw:      raw,
		})
		items = append(items, session.Item{
			ID:       fmt.Sprintf("sender-assistant-%d", index),
			Type:     "message",
			Role:     "assistant",
			Text:     turn.assistant,
			Metadata: map[string]any{"turnId": fmt.Sprintf("sender-turn-%d", index)},
		})
	}
	if _, err := router.runtimeAppendItems(sender, items); err != nil {
		t.Fatalf("runtimeAppendItems(sender) error = %v", err)
	}

	// The oldest exchange is dropped, so the snapshot carries turns 1..3: each
	// prompt keeps one role label per line and is preceded by the assistant reply
	// that answers or precedes it.
	indented := fmt.Sprintf("<codex_delegation>\n  <source_thread_id>%s</source_thread_id>\n  <input>Inspect.</input>\n</codex_delegation>", sender)
	compact := fmt.Sprintf("<codex_delegation><source_thread_id>%s</source_thread_id><input>Inspect.</input></codex_delegation>", sender)
	capturedLines := []string{
		"assistant: I can inspect the experiment.",
		"user: Inspect the experiment.",
		"assistant: Rerun only the staging task?",
		"assistant: Preserve its checkpoints?",
		"user: Add twenty workers.",
		"assistant: I will use staging.",
		"user: Only use staging.",
		"user: Never production.",
	}
	for _, testCase := range []struct {
		name      string
		receiver  session.ThreadID
		namespace string
		toolName  string
		output    string
		wantLines []string
		wantSourc string
	}{
		{
			name:      "trusted delegation captures sender instructions and assistant context",
			receiver:  receiverApp,
			namespace: "codex_app",
			toolName:  "send_message_to_thread",
			output:    indented,
			wantLines: capturedLines,
			wantSourc: "Source thread: " + string(sender) + "\n",
		},
		{
			name:      "cloud-thread producer's compact envelope keeps provenance",
			receiver:  receiverCloud,
			namespace: "cloud_threads",
			toolName:  "send_message",
			output:    compact,
			wantLines: capturedLines,
			wantSourc: "Source thread: " + string(sender) + "\n",
		},
		{
			name:      "recognized delivery without provenance",
			receiver:  receiverTUI,
			namespace: "codex_tui",
			toolName:  "send_message_to_thread",
			output:    "Inspect again without sender provenance.",
			wantLines: nil,
			wantSourc: "Source thread: unavailable\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			delivery := session.Item{
				ID:        "delivery-" + testCase.namespace,
				Type:      "function_call_output",
				Name:      testCase.toolName,
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
				if strings.HasPrefix(line, "user: ") || strings.HasPrefix(line, "assistant: ") {
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

// Rust #49951 shares one 900-byte budget per exchange and keeps user evidence
// first, so assistant context that no longer fits is dropped whole and reported
// as unavailable context instead of crowding out the instruction.
func TestRuntimeRouterSenderCaptureSharesExchangeBudgetLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	sender := session.ThreadID("thread-sender")
	receiver := session.ThreadID("thread-receiver")
	for _, id := range []session.ThreadID{sender, receiver} {
		if err := store.Create(&session.Record{ID: id, SessionID: string(id)}); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})

	padded := "Inspect the experiment." + strings.Repeat("x", 850)
	raw, err := json.Marshal(map[string]any{
		"type":    "message",
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": padded}},
		"internal_chat_message_metadata_passthrough": map[string]any{"content_item_kinds": []string{"user.text"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.runtimeAppendItems(sender, []session.Item{
		// The assistant turn precedes the instruction it is context for.
		{ID: "sender-assistant-0", Type: "message", Role: "assistant", Text: "Rerun only the staging task?", Metadata: map[string]any{"turnId": "sender-turn-0"}},
		{ID: "sender-user-0", Type: "message", Role: "user", Text: padded, Raw: raw, Metadata: map[string]any{"turnId": "sender-turn-0"}},
	}); err != nil {
		t.Fatalf("runtimeAppendItems(sender) error = %v", err)
	}

	delivery := session.Item{
		ID:        "delivery-budget",
		Type:      "function_call_output",
		Name:      "send_message_to_thread",
		Namespace: "codex_app",
		Text:      fmt.Sprintf("<codex_delegation>\n  <source_thread_id>%s</source_thread_id>\n  <input>Inspect.</input>\n</codex_delegation>", sender),
	}
	if _, err := router.runtimeAppendItems(receiver, []session.Item{delivery}); err != nil {
		t.Fatalf("runtimeAppendItems(receiver) error = %v", err)
	}
	router.retainedContextsMu.Lock()
	context := router.retainedLiveContextLocked(string(receiver))
	router.retainedContextsMu.Unlock()
	if context == nil || context.SenderUserMessages() == nil {
		t.Fatal("receiver retained context holds no sender delivery")
	}
	text := context.SenderUserMessages().Text
	if !strings.Contains(text, "user: "+padded+"\n") {
		t.Fatalf("padded instruction missing from snapshot:\n%s", text)
	}
	if strings.Contains(text, "assistant: Rerun only the staging task?") {
		t.Fatalf("assistant context leaked past the shared exchange budget:\n%s", text)
	}
	if !strings.Contains(text, state.RootMessage{Kind: state.RootMessageIncompleteAssistantContext}.Render()) {
		t.Fatalf("missing unavailable-context notice:\n%s", text)
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
		{ID: "cloud-other-name", Type: "function_call_output", Name: "send_message_to_thread", Namespace: "cloud_threads", Text: "ok"},
	} {
		if _, ok := router.captureSenderUserMessages(&item, "thread-x", "turn-x"); ok {
			t.Fatalf("item %s should not capture sender context", item.ID)
		}
	}
}
