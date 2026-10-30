package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/session"
)

// Mirrors Rust #47584: a user reply accepted before the streamed assistant
// message completes cannot be ordered ahead of the question in retained
// context.
func TestRetainedAssistantOrderReservedAtStreamStartLikeRust(t *testing.T) {
	context := &retainedctx.RetainedContext{}
	userOrder := context.ReserveOrder()
	reserved := context.ReserveAssistantMessageOrder("assistant-1")
	if reserved <= userOrder {
		t.Fatalf("assistant reservation = %d, want after the preceding user %d", reserved, userOrder)
	}
	replyOrder := context.ReserveOrder()

	assistantID := "assistant-1"
	record := retainedSessionItemRecord{
		assistant: true,
		message: retainedctx.RetainedUserMessage{
			Text: "question", MessageID: &assistantID, Complete: true,
		},
	}
	if _, order, ok := recordRetainedSessionItem(context, record, nil); !ok || order != reserved {
		t.Fatalf("recorded assistant order = %d, %v; want reservation %d", order, ok, reserved)
	}
	replyID := "reply-1"
	source := retainedctx.LocalInputSource(&replyOrder)
	context.RecordUserMessage(retainedctx.RetainedUserMessage{
		Text: "reply", MessageID: &replyID, Complete: true,
	}, source)
	if got, ok := context.AssistantMessageOrder(&assistantID); !ok || got != reserved {
		t.Fatalf("assistant order = %d, %v; want %d", got, ok, reserved)
	}
	if got, ok := context.UserMessageOrder(&replyID); !ok || got != replyOrder {
		t.Fatalf("reply order = %d, %v; want %d", got, ok, replyOrder)
	}
	if reserved >= replyOrder {
		t.Fatalf("assistant order %d is not before reply order %d", reserved, replyOrder)
	}
}

// Appending the completed assistant item must consume the stream-start
// reservation instead of minting a new position after the queued reply.
func TestRuntimeAppendPreservesReservedAssistantOrderLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-stream-order")
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
	router.reserveAssistantRetainedOrder(string(threadID), "assistant-question")
	reply := sessionItemForRetainedTest("user-2", "Yes, continue.", []string{"user.text"}, []string{"input_text"})
	reply.CreatedAt = now.Add(time.Second)
	if _, err := router.runtimeAppendItems(threadID, []session.Item{reply}); err != nil {
		t.Fatalf("runtimeAppendItems(reply) error = %v", err)
	}
	assistant := session.Item{
		ID: "assistant-question", Type: "message", Role: "assistant", Text: "Continue?",
		CreatedAt: now.Add(2 * time.Second), Metadata: map[string]any{"turnId": "turn-question"},
	}
	if _, err := router.runtimeAppendItems(threadID, []session.Item{assistant}); err != nil {
		t.Fatalf("runtimeAppendItems(assistant) error = %v", err)
	}

	record, err := store.Read(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("Read() = %#v/%v", record, err)
	}
	orders := map[string]uint64{}
	for index := range record.Items {
		item := &record.Items[index]
		if metadata := harnessMetadataFromSessionItem(item); metadata != nil && metadata.UserInputOrder != nil {
			orders[item.ID] = *metadata.UserInputOrder
		}
	}
	assistantOrder, ok := orders[assistant.ID]
	if !ok {
		t.Fatalf("assistant item has no recorded order: %#v", record.Items)
	}
	replyOrder, replyRecorded := orders[reply.ID]
	if !replyRecorded || assistantOrder >= replyOrder {
		t.Fatalf("assistant order = %d, reply order = %d/%v; want assistant first", assistantOrder, replyOrder, replyRecorded)
	}
	if got, want := retainedOrderedTexts(router.retainedContextForThread(string(threadID)).OrderedEntries()),
		[]string{"Only deploy privately.", "Continue?", "Yes, continue."}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("retained order = %#v, want %#v", got, want)
	}
}
