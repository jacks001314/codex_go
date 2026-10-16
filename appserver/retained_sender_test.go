package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/rollout"
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
			name:      "desktop create-thread delivery captures sender instructions",
			receiver:  receiverApp,
			namespace: "codex_app",
			toolName:  "create_thread",
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

// A paired tool result, an unregistered tool name and a namespace/name pair
// Rust does not list never capture, matching Rust's destructuring (`call_id:
// None`, name/namespace match). `create_thread` is registered for `codex_app`
// only (Rust #54590), so the TUI namespace still rejects it.
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
		{ID: "tui-create-thread", Type: "function_call_output", Name: "create_thread", Namespace: "codex_tui", Text: "ok"},
		{ID: "other-name", Type: "function_call_output", Name: "fork_thread", Namespace: "codex_app", Text: "ok"},
		{ID: "other-namespace", Type: "function_call_output", Name: "send_message_to_thread", Namespace: "untrusted", Text: "ok"},
		{ID: "cloud-other-name", Type: "function_call_output", Name: "send_message_to_thread", Namespace: "cloud_threads", Text: "ok"},
	} {
		if _, ok := router.captureSenderUserMessages(&item, "thread-x", "turn-x"); ok {
			t.Fatalf("item %s should not capture sender context", item.ID)
		}
	}
}

// senderInstructionRaw is the response-item shape a genuine user instruction
// carries: the harness classifies every content entry as user text, which is
// what `retainedRecordForSessionItem` requires before it proves completeness.
func senderInstructionRaw(text string) json.RawMessage {
	raw, err := json.Marshal(map[string]any{
		"type":    "message",
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": text}},
		"internal_chat_message_metadata_passthrough": map[string]any{"content_item_kinds": []string{"user.text"}},
	})
	if err != nil {
		return nil
	}
	return raw
}

// senderDelegationDelivery is the `cloud_threads.send_message` delivery the Rust
// scenario's cloud producer serializes.
func senderDelegationDelivery(id string, sourceThreadID string) session.Item {
	return session.Item{
		ID:        id,
		Type:      "function_call_output",
		Name:      "send_message",
		Namespace: "cloud_threads",
		Text:      fmt.Sprintf("<codex_delegation><source_thread_id>%s</source_thread_id><input>Inspect.</input></codex_delegation>", sourceThreadID),
	}
}

// writeSenderRollout records a sender's history the way another host's worker
// would: the durable rollout is the only copy, and the last turn is rolled back.
func writeSenderRollout(t *testing.T, home string, threadID string, source string, turns []struct{ user, assistant string }, rolledBackTurns uint32) {
	t.Helper()
	now := time.Now().UTC()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     home,
		ThreadID:      threadID,
		Source:        source,
		CWD:           home,
		ModelProvider: "openai",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder(%s) error = %v", threadID, err)
	}
	for index, turn := range turns {
		turnID := fmt.Sprintf("sender-turn-%d", index)
		if err := recorder.AppendItem(rollout.Item{
			ID:       fmt.Sprintf("sender-user-%d", index),
			Type:     "message",
			Role:     "user",
			Text:     turn.user,
			Raw:      senderInstructionRaw(turn.user),
			Metadata: map[string]any{"turnId": turnID},
		}); err != nil {
			t.Fatalf("AppendItem(user %d) error = %v", index, err)
		}
		if turn.assistant == "" {
			continue
		}
		if err := recorder.AppendItem(rollout.Item{
			ID:       fmt.Sprintf("sender-assistant-%d", index),
			Type:     "message",
			Role:     "assistant",
			Text:     turn.assistant,
			Metadata: map[string]any{"turnId": turnID},
		}); err != nil {
			t.Fatalf("AppendItem(assistant %d) error = %v", index, err)
		}
	}
	if rolledBackTurns > 0 {
		if err := recorder.AppendThreadRolledBack(rolledBackTurns, now); err != nil {
			t.Fatalf("AppendThreadRolledBack error = %v", err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close(%s) error = %v", threadID, err)
	}
}

// Mirrors Rust #51734 (`capture_sender_user_messages`, sender_context.rs:67-111
// and `GuardianSenderMessages` at guardian_sender_messages.rs:52-54): when the
// receiver never loaded the sender, the sender's history is read through the
// persisted thread store without resuming it, rollback is respected, and the
// evidence is labelled so the reviewer knows newer unpersisted messages may be
// missing. The vectors are Rust's own cross-host scenario
// (tests/suite/scenarios_guardian_sender_context.rs:59-160).
func TestRuntimeRouterReadsPersistedSenderContextLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	sender := "thread-sender-persisted"
	receiver := "thread-receiver"
	if err := store.Create(&session.Record{ID: session.ThreadID(receiver), SessionID: receiver}); err != nil {
		t.Fatalf("Create(receiver) error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(home)})
	// Neither manager loads the other (Rust asserts
	// `receiver.thread_manager.get_thread(sender_id).is_err()`).
	if router.threads.HasLiveThread(session.ThreadID(sender)) {
		t.Fatal("sender must not be loaded in the receiver's runtime")
	}
	writeSenderRollout(t, home, sender, "cli", []struct{ user, assistant string }{
		{user: "Inspect the staging job.", assistant: "Rerun only staging, preserving checkpoints?"},
		{user: "Yes. Staging only; never production.", assistant: "I will ask the helper."},
		{user: "Production is also allowed."},
	}, 1)

	snapshot, ok := router.captureSenderUserMessages(senderPointer(senderDelegationDelivery("delivery-0", sender)), receiver, "receiver-turn-0")
	if !ok {
		t.Fatal("capture did not recognize the delivery")
	}
	var lines []string
	for _, line := range strings.Split(snapshot.Text, "\n") {
		if strings.HasPrefix(line, "user: ") || strings.HasPrefix(line, "assistant: ") {
			lines = append(lines, line)
		}
	}
	want := []string{
		"user: Inspect the staging job.",
		"assistant: Rerun only staging, preserving checkpoints?",
		"user: Yes. Staging only; never production.",
	}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("persisted sender lines = %#v, want %#v\nsnapshot:\n%s", lines, want, snapshot.Text)
	}
	// The rolled-back turn's instruction must never reach the reviewer.
	if strings.Contains(snapshot.Text, "Production is also allowed.") {
		t.Fatalf("rolled-back sender turn leaked into evidence:\n%s", snapshot.Text)
	}
	if !strings.Contains(snapshot.Text, "Host: Read from persisted sender history; newer unpersisted messages may be missing.\n") {
		t.Fatalf("persisted read is not labelled:\n%s", snapshot.Text)
	}
	if !strings.HasSuffix(snapshot.Text, ">>> SENDER USER MESSAGES END\n") {
		t.Fatalf("snapshot markers = %q", snapshot.Text)
	}
	if len(snapshot.Text) > 3600 {
		t.Fatalf("snapshot length = %d, want <= 3600", len(snapshot.Text))
	}
}

// Mirrors the two branches Rust #51734 keeps apart: a sender loaded in this
// runtime supplies its live retained context and is not labelled persisted,
// while an internal host session never lends a delegation its instructions
// (`meta.source.is_internal()`, protocol/src/protocol.rs:3016-3018).
func TestRuntimeRouterSenderCaptureLabelsOnlyUnloadedSendersLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	receiver := "thread-receiver"
	liveSender := "thread-live-sender"
	internalSender := "thread-internal-sender"
	for _, id := range []string{receiver, liveSender} {
		if err := store.Create(&session.Record{ID: session.ThreadID(id), SessionID: id}); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(home)})
	liveRecord, err := store.Read(session.ThreadID(liveSender), false, false)
	if err != nil {
		t.Fatalf("Read(live sender) error = %v", err)
	}
	if err := router.services.ThreadRouter.retainLiveThread(liveRecord); err != nil {
		t.Fatalf("retainLiveThread error = %v", err)
	}
	if !router.threads.HasLiveThread(session.ThreadID(liveSender)) {
		t.Fatal("live sender was not retained")
	}
	if _, err := router.runtimeAppendItems(session.ThreadID(liveSender), []session.Item{{
		ID:       "live-user-0",
		Type:     "message",
		Role:     "user",
		Text:     "Live instruction.",
		Raw:      senderInstructionRaw("Live instruction."),
		Metadata: map[string]any{"turnId": "live-turn-0"},
	}}); err != nil {
		t.Fatalf("runtimeAppendItems(live sender) error = %v", err)
	}
	live, ok := router.captureSenderUserMessages(senderPointer(senderDelegationDelivery("delivery-live", liveSender)), receiver, "receiver-turn-1")
	if !ok {
		t.Fatal("live delivery was not recognized")
	}
	if !strings.Contains(live.Text, "user: Live instruction.\n") {
		t.Fatalf("live sender evidence missing:\n%s", live.Text)
	}
	if strings.Contains(live.Text, "Read from persisted sender history") {
		t.Fatalf("loaded sender was wrongly labelled persisted:\n%s", live.Text)
	}

	// An internal host session is persisted but ineligible as a sender.
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(internalSender),
		SessionID: internalSender,
		Metadata:  session.Metadata{Source: internalMemorySessionSource},
	}); err != nil {
		t.Fatalf("Create(internal sender) error = %v", err)
	}
	if _, err := router.runtimeAppendItems(session.ThreadID(internalSender), []session.Item{{
		ID:       "internal-user-0",
		Type:     "message",
		Role:     "user",
		Text:     "Internal instruction.",
		Raw:      senderInstructionRaw("Internal instruction."),
		Metadata: map[string]any{"turnId": "internal-turn-0"},
	}}); err != nil {
		t.Fatalf("runtimeAppendItems(internal sender) error = %v", err)
	}
	internal, ok := router.captureSenderUserMessages(senderPointer(senderDelegationDelivery("delivery-internal", internalSender)), receiver, "receiver-turn-1")
	if !ok {
		t.Fatal("internal-source delivery was not recognized")
	}
	if strings.Contains(internal.Text, "Internal instruction.") {
		t.Fatalf("internal session evidence leaked into a delegation:\n%s", internal.Text)
	}
	if !strings.Contains(internal.Text, "Host: No sender user messages are available.\n") {
		t.Fatalf("refused internal read should report no evidence:\n%s", internal.Text)
	}
	if strings.Contains(internal.Text, "Read from persisted sender history") {
		t.Fatalf("a refused read must not be labelled persisted:\n%s", internal.Text)
	}
}

func senderPointer(item session.Item) *session.Item { return &item }
