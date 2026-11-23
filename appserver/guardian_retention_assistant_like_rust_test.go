package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/utils"
)

// newRetentionThreadFixture creates one thread with a rollout and the given
// Guardian retention policy on its owned settings snapshot (Rust #62828).
func newRetentionThreadFixture(t *testing.T, items []session.Item, retention *retainedctx.GuardianRetentionPolicy) (*RuntimeRouter, *session.Record) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	cwd := t.TempDir()
	threadID := session.ThreadID("thread-retention-messages")
	record := &session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Items:    items,
		Metadata: session.Metadata{CWD: cwd, Model: "gpt-5", ApprovalPolicy: "never", Extra: map[string]any{}},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	threadRouter := NewRouter(store)
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	if err := threadRouter.appendThreadSettingsAppliedSnapshot(threadID, rollout.ThreadSettingsApplied{
		ApprovalPolicy:    "never",
		CWD:               cwd,
		GuardianRetention: retention,
	}, now); err != nil {
		t.Fatalf("appendThreadSettingsAppliedSnapshot() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, Config: config.NewConfigService(t.TempDir())})
	t.Cleanup(func() { _ = router.Close() })
	return router, record
}

// Rust #62828: a thread with an Elastic record limit keeps the whole assistant
// message instead of the fixed 900-token projection.
func TestRetainedAssistantMessagesKeepElasticOriginalsLikeRust(t *testing.T) {
	longText := strings.Repeat("a", utils.ApproxBytesForTokens(2000))
	assistant := session.Item{
		ID: "assistant-1", Type: "message", Role: "assistant", Text: longText,
		Metadata: map[string]any{"turnId": "turn-1"},
	}
	limit := utils.ApproxBytesForTokens(4000)
	router, record := newRetentionThreadFixture(t, []session.Item{assistant}, &retainedctx.GuardianRetentionPolicy{AssistantRecordBytes: &limit})
	context := router.retainedContextForThread(string(record.ID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	if !context.RetainsOriginalAssistantMessages() {
		t.Fatal("thread policy was not restored from the settings snapshot")
	}
	var found bool
	for _, entry := range context.OrderedEntries() {
		if entry.Entry.AssistantMessage == nil {
			continue
		}
		found = true
		if entry.Entry.AssistantMessage.Text != longText {
			t.Fatalf("retained assistant text length = %d, want the whole %d-byte message", len(entry.Entry.AssistantMessage.Text), len(longText))
		}
		if !entry.Entry.AssistantMessage.Complete {
			t.Fatal("whole assistant message recorded incomplete")
		}
	}
	if !found {
		t.Fatalf("retained assistant message missing: %#v", context.OrderedEntries())
	}

	// The same history under Fixed mode keeps the bounded projection.
	fixedRouter, fixedRecord := newRetentionThreadFixture(t, []session.Item{assistant}, nil)
	fixed := fixedRouter.retainedContextForThread(string(fixedRecord.ID))
	for _, entry := range fixed.OrderedEntries() {
		if entry.Entry.AssistantMessage != nil && len(entry.Entry.AssistantMessage.Text) >= len(longText) {
			t.Fatalf("fixed-mode retained assistant text length = %d, want a bounded projection", len(entry.Entry.AssistantMessage.Text))
		}
	}
}

// Rust #62828: an oversized confirmed delivery keeps only an incomplete marker
// on the tool output, while a delivery within the limit keeps its text.
func TestOversizedConfirmedDeliveryKeepsIncompleteMarkerLikeRust(t *testing.T) {
	limit := 300
	call := session.Item{ID: "call-1", Type: "function_call", CallID: "call-1", Name: "send_message_to_thread", Namespace: "codex_app", Text: "{}",
		Data: map[string]any{harnessMetadataKey: json.RawMessage(`{"user_input_order":0}`)}}
	delivery := func(id string, callID string, text string) session.Item {
		raw, err := json.Marshal(map[string]any{"delivered_assistant_message": text})
		if err != nil {
			t.Fatal(err)
		}
		return session.Item{
			ID: id, Type: "function_call_output", CallID: callID, Name: "send_message_to_thread", Namespace: "codex_app",
			Text: "Sent.", Data: map[string]any{harnessMetadataKey: json.RawMessage(raw)},
		}
	}
	oversized := delivery("out-big", "call-1", strings.Repeat("d", 2000))
	router, record := newRetentionThreadFixture(t, []session.Item{call}, &retainedctx.GuardianRetentionPolicy{AssistantRecordBytes: &limit})
	router.annotateRetainedHarnessMetadata(record.ID, []session.Item{oversized})

	metadata := harnessMetadataFromSessionItem(&oversized)
	if metadata == nil || metadata.DeliveredAssistantMessage != nil || !metadata.DeliveredAssistantMessageIncomplete {
		t.Fatalf("oversized delivery metadata = %#v, want only the incomplete marker", metadata)
	}
	context := router.retainedContextForThread(string(record.ID))
	var incomplete bool
	for _, entry := range context.OrderedEntries() {
		if message := entry.Entry.AssistantMessage; message != nil {
			if message.Text != "" || message.Complete {
				t.Fatalf("oversized retained delivery = %#v, want an incomplete marker", message)
			}
			incomplete = true
		}
	}
	if !incomplete {
		t.Fatal("oversized delivery did not retain an incomplete marker")
	}

	// A delivery that fits the configured limit keeps its confirmed text.
	small := delivery("out-small", "call-1", "short answer")
	router.annotateRetainedHarnessMetadata(record.ID, []session.Item{small})
	metadata = harnessMetadataFromSessionItem(&small)
	if metadata == nil || metadata.DeliveredAssistantMessage == nil || *metadata.DeliveredAssistantMessage != "short answer" || metadata.DeliveredAssistantMessageIncomplete {
		t.Fatalf("within-limit delivery metadata = %#v", metadata)
	}
}

// Rust #62828: an omitted confirmed delivery contributes no assistant text and
// marks the root assistant context missing.
func TestGuardianRootDeliveredTextsSkipIncompleteDeliveriesLikeRust(t *testing.T) {
	delivery := func(id string, callID string, metadata map[string]any) session.Item {
		raw, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		return session.Item{
			ID: id, Type: "function_call_output", CallID: callID, Name: "send_message_to_thread", Namespace: "codex_app",
			Text: "Sent.", Data: map[string]any{harnessMetadataKey: json.RawMessage(raw)},
		}
	}
	items := []session.Item{
		{ID: "call-big", Type: "function_call", CallID: "call-big", Name: "send_message_to_thread", Namespace: "codex_app"},
		delivery("out-big", "call-big", map[string]any{"delivered_assistant_message_incomplete": true}),
		{ID: "call-small", Type: "function_call", CallID: "call-small", Name: "send_message_to_thread", Namespace: "codex_app"},
		delivery("out-small", "call-small", map[string]any{"delivered_assistant_message": "short answer"}),
	}
	texts, missing := guardianRootDeliveredTexts(items)
	if !missing {
		t.Fatal("incomplete delivery did not mark the assistant context missing")
	}
	if texts["call-big"] != "" {
		t.Fatalf("incomplete delivery contributed text %q", texts["call-big"])
	}
	if texts["call-small"] != "short answer" {
		t.Fatalf("confirmed delivery text = %q", texts["call-small"])
	}
}
