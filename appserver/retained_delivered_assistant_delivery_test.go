package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

func userMessagingToolExecution(delivered string, success bool, now time.Time) *turn.ToolExecutionResult {
	return &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "message-call",
			ToolName: tool.NamespacedName("mcp__codex_apps", "user_messaging_send_message"),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"text":"Continue?"}`},
		},
		Output: &tool.Output{
			CallID:                    "message-call",
			ToolName:                  tool.NamespacedName("mcp__codex_apps", "user_messaging_send_message"),
			Success:                   success,
			Body:                      "Message sent.",
			Data:                      map[string]any{"tool": "user_messaging_send_message"},
			CompletedAt:               now,
			DeliveredAssistantMessage: delivered,
		},
		StartedAt:  now,
		FinishedAt: now,
	}
}

// Rust #47030 records the host-confirmed User Messaging text on the tool output,
// keeps the question at the call's own accepted position, and preserves that
// ordering after resume even when the reply and output arrive out of order.
func TestRuntimeRouterRetainsDeliveredAssistantFromToolOutputLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-delivered-output")
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID)}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	now := fixedTime()

	initial := sessionItemForRetainedTest("user-1", "Only deploy privately.", []string{"user.text"}, []string{"input_text"})
	initial.CreatedAt = now
	if _, err := router.runtimeAppendItems(threadID, []session.Item{initial}); err != nil {
		t.Fatalf("runtimeAppendItems(initial) error = %v", err)
	}

	execution := userMessagingToolExecution("Continue?", true, now)
	call, ok := sessionItemForAppToolCall("turn-question", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForAppToolCall() = false")
	}
	output, ok := sessionItemForAppToolOutput("turn-question", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForAppToolOutput() = false")
	}
	if output.Type != "tool_output" {
		t.Fatalf("output type = %q, want the runtime tool_output form", output.Type)
	}
	outputMetadata := harnessMetadataFromSessionItem(&output)
	if outputMetadata == nil || outputMetadata.DeliveredAssistantMessage == nil ||
		*outputMetadata.DeliveredAssistantMessage != "Continue?" {
		t.Fatalf("tool output metadata = %#v", output.Data[harnessMetadataKey])
	}

	if _, err := router.runtimeAppendItems(threadID, []session.Item{call}); err != nil {
		t.Fatalf("runtimeAppendItems(call) error = %v", err)
	}
	reply := sessionItemForRetainedTest("user-2", "Yes, continue.", []string{"user.text"}, []string{"input_text"})
	reply.CreatedAt = now.Add(time.Second)
	if _, err := router.runtimeAppendItems(threadID, []session.Item{reply}); err != nil {
		t.Fatalf("runtimeAppendItems(reply) error = %v", err)
	}
	if _, err := router.runtimeAppendItems(threadID, []session.Item{output}); err != nil {
		t.Fatalf("runtimeAppendItems(output) error = %v", err)
	}

	record, err := store.Read(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("Read() = %#v/%v", record, err)
	}
	orders := map[string]uint64{}
	for index := range record.Items {
		item := &record.Items[index]
		metadata := harnessMetadataFromSessionItem(item)
		if metadata != nil && metadata.UserInputOrder != nil {
			orders[item.ID] = *metadata.UserInputOrder
		}
	}
	if _, ok := orders[call.ID]; !ok {
		t.Fatalf("function call has no shared acceptance order: %#v", record.Items)
	}
	if orders[call.ID] >= orders[reply.ID] {
		t.Fatalf("call order = %d, reply order = %d, want the call first", orders[call.ID], orders[reply.ID])
	}

	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	want := []string{"Only deploy privately.", "Continue?", "Yes, continue."}
	if got := retainedOrderedTexts(context.OrderedEntries()); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("retained order = %#v, want %#v", got, want)
	}
	for _, entry := range context.OrderedEntries() {
		if entry.Entry.AssistantMessage != nil && entry.Entry.AssistantMessage.TurnID != "turn-question" {
			t.Fatalf("delivered assistant turn = %q, want the call's turn", entry.Entry.AssistantMessage.TurnID)
		}
	}

	// A fresh router rebuilds the delivered evidence and the shared counter from
	// persisted metadata rather than losing it with the live in-memory context.
	resumed := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	resumedContext := resumed.retainedContextForThread(string(threadID))
	if resumedContext == nil {
		t.Fatal("resumed retainedContextForThread() = nil")
	}
	if got := retainedOrderedTexts(resumedContext.OrderedEntries()); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("resumed retained order = %#v, want %#v", got, want)
	}
	next := session.Item{ID: "assistant-2", Type: "message", Role: "assistant", Text: "Final answer.", CreatedAt: now.Add(2 * time.Second)}
	if _, err := resumed.runtimeAppendItems(threadID, []session.Item{next}); err != nil {
		t.Fatalf("runtimeAppendItems(next) error = %v", err)
	}
	record, err = store.Read(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("Read() after next = %#v/%v", record, err)
	}
	for index := range record.Items {
		item := &record.Items[index]
		if item.ID != next.ID {
			continue
		}
		metadata := harnessMetadataFromSessionItem(item)
		if metadata == nil || metadata.UserInputOrder == nil || *metadata.UserInputOrder <= orders[reply.ID] {
			t.Fatalf("next assistant order = %#v, want greater than %d", metadata, orders[reply.ID])
		}
		return
	}
	t.Fatalf("next assistant item missing from history: %#v", record.Items)
}

func TestAppToolOutputSkipsDeliveredTextForFailedSendLikeRust(t *testing.T) {
	now := fixedTime()
	execution := userMessagingToolExecution("This must not be retained.", false, now)
	item, ok := sessionItemForAppToolOutput("turn-failed", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForAppToolOutput() = false")
	}
	if metadata := harnessMetadataFromSessionItem(&item); metadata != nil && metadata.DeliveredAssistantMessage != nil {
		t.Fatalf("failed send carried delivered assistant metadata: %#v", metadata)
	}
}
