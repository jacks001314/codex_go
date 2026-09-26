package retainedctx

import "testing"

// AssistantMessageOrder exposes the recorded acceptance order of an assistant
// message so a re-derivation can reuse it instead of advancing the counter
// (Rust reserves that order once in Session::reserve_assistant_message_order).
func TestAssistantMessageOrderReportsRecordedOrderLikeRust(t *testing.T) {
	context := &RetainedContext{}
	if _, ok := context.AssistantMessageOrder(nil); ok {
		t.Fatal("a nil message id reported an order")
	}
	messageID := "assistant-1"
	if _, ok := context.AssistantMessageOrder(&messageID); ok {
		t.Fatal("an unrecorded message id reported an order")
	}
	order := context.ReserveOrder()
	context.RecordAssistantMessage(RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: &messageID,
		Text:      "Understood.",
		Complete:  true,
	}, LocalInputSource(&order))
	got, ok := context.AssistantMessageOrder(&messageID)
	if !ok || got != order {
		t.Fatalf("AssistantMessageOrder() = %d/%v, want %d/true", got, ok, order)
	}
	// An unsequenced source records nothing, so it leaves no order behind.
	otherID := "assistant-2"
	context.RecordAssistantMessage(RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: &otherID,
		Text:      "Unsequenced.",
		Complete:  true,
	}, LocalInputSource(nil))
	if _, ok := context.AssistantMessageOrder(&otherID); ok {
		t.Fatal("an unsequenced assistant message reported an order")
	}
}
