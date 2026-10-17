package appserver

import (
	"testing"

	"codex_go/retainedctx"
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
