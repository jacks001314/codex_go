package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/session"
)

// Mirrors the delivered-assistant arm of Rust's
// `ContextManager::record_retained_message`: the bounded text a successful
// messaging tool result confirmed is retained as an assistant message at the
// *call's* position, not at the later output position, and an inherited call
// sourced from a worker is left out.
func TestRuntimeRouterRetainsDeliveredAssistantMessageLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-delivered")
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID)}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})

	callMetadata, err := json.Marshal(map[string]any{"user_input_order": uint64(3)})
	if err != nil {
		t.Fatal(err)
	}
	call := session.Item{
		ID: "call-message", Type: "function_call", Name: "user_messaging__send_message", CallID: "call-message",
		Text:     `{"text":"Continue?"}`,
		Metadata: map[string]any{"turnId": "turn-call"},
		Data:     map[string]any{harnessMetadataKey: json.RawMessage(callMetadata)},
	}
	if _, err := router.runtimeAppendItems(threadID, []session.Item{call}); err != nil {
		t.Fatalf("runtimeAppendItems(call) error = %v", err)
	}

	delivered := "Continue?"
	outputMetadata, err := json.Marshal(map[string]any{"delivered_assistant_message": delivered})
	if err != nil {
		t.Fatal(err)
	}
	output := session.Item{
		ID: "output-message", Type: "function_call_output", CallID: "call-message", Name: "user_messaging__send_message",
		Text:     "sent",
		Metadata: map[string]any{"turnId": "turn-output"},
		Data:     map[string]any{harnessMetadataKey: json.RawMessage(outputMetadata)},
	}
	if _, err := router.runtimeAppendItems(threadID, []session.Item{output}); err != nil {
		t.Fatalf("runtimeAppendItems(output) error = %v", err)
	}

	router.retainedContextsMu.Lock()
	context := router.retainedLiveContextLocked(string(threadID))
	router.retainedContextsMu.Unlock()
	if context == nil {
		t.Fatal("retained context = nil")
	}
	var assistant []*retainedctx.RetainedUserMessage
	for _, entry := range context.OrderedEntries() {
		if entry.Entry.AssistantMessage != nil {
			assistant = append(assistant, entry.Entry.AssistantMessage)
		}
	}
	if len(assistant) != 1 || assistant[0].Text != delivered {
		t.Fatalf("assistant messages = %#v, want the delivered text", assistant)
	}
	if assistant[0].MessageID == nil || *assistant[0].MessageID != "call-message" || assistant[0].TurnID != "turn-call" {
		t.Fatalf("assistant record = %#v, want the call's identity and turn", assistant[0])
	}
}

// A worker's history carries the parent's adopted calls, so an inherited call's
// delivered text must not become the worker's own assistant evidence
// (`RetainedInputSource::Inherited` is skipped unless the thread is a root).
func TestRuntimeRouterSkipsInheritedDeliveredAssistantMessageForWorkerLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-worker")
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID),
		Metadata: session.Metadata{Originator: "subagent"},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})

	callMetadata, err := json.Marshal(map[string]any{"user_input_order": uint64(3), "inherited_user_message": true})
	if err != nil {
		t.Fatal(err)
	}
	call := session.Item{
		ID: "inherited-call", Type: "function_call", Name: "user_messaging__send_message", CallID: "inherited-call",
		Metadata: map[string]any{"turnId": "parent-turn"},
		Data:     map[string]any{harnessMetadataKey: json.RawMessage(callMetadata)},
	}
	if _, err := router.runtimeAppendItems(threadID, []session.Item{call}); err != nil {
		t.Fatalf("runtimeAppendItems(call) error = %v", err)
	}
	delivered := "Continue?"
	outputMetadata, err := json.Marshal(map[string]any{"delivered_assistant_message": delivered})
	if err != nil {
		t.Fatal(err)
	}
	output := session.Item{
		ID: "inherited-output", Type: "function_call_output", CallID: "inherited-call",
		Text: "sent",
		Data: map[string]any{harnessMetadataKey: json.RawMessage(outputMetadata)},
	}
	if _, err := router.runtimeAppendItems(threadID, []session.Item{output}); err != nil {
		t.Fatalf("runtimeAppendItems(output) error = %v", err)
	}

	router.retainedContextsMu.Lock()
	context := router.retainedLiveContextLocked(string(threadID))
	router.retainedContextsMu.Unlock()
	if context == nil {
		t.Fatal("retained context = nil")
	}
	for _, entry := range context.OrderedEntries() {
		if entry.Entry.AssistantMessage != nil {
			t.Fatalf("worker retained the parent's delivered text: %#v", entry.Entry.AssistantMessage)
		}
	}
}
