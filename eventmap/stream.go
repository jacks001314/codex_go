package eventmap

import (
	"strings"
)

type FinalizedFacts struct {
	LastAgentMessage                string
	DefersMailboxDeliveryToNextTurn bool
	MemoryCitation                  string
}

func LastAssistantMessageFromItem(item *ResponseItem, planMode bool) (string, bool) {
	text, ok := RawAssistantOutputTextFromItem(item)
	if !ok || text == "" {
		return "", false
	}
	text = StripHiddenAssistantMarkup(text, planMode)
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

// CompletedItemDefersMailboxDeliveryToNextTurn mirrors Rust
// codex-rs/core/src/stream_events_utils.rs
// `completed_item_defers_mailbox_delivery_to_next_turn`: only a completed
// assistant message that carries a user-visible answer (phase `final_answer`, or
// untagged) defers queued mailbox mail to the next turn. Rust #51249 extended
// the deferral rule's complement, so `commentary` and `partial_answer`
// fragments keep mailbox delivery open instead of closing the turn, and every
// non-message item (tool calls, image generation, reasoning) falls through to
// the `_ => false` arm. Untagged messages count as final-answer text so
// untagged providers default to the safer "defer mailbox mail" behavior.
func CompletedItemDefersMailboxDeliveryToNextTurn(item *ResponseItem, planMode bool) bool {
	if item == nil {
		return false
	}
	if item.Kind != ResponseMessage || item.Role != "assistant" {
		return false
	}
	if item.Phase != "" && item.Phase != "final_answer" {
		return false
	}
	_, ok := LastAssistantMessageFromItem(item, planMode)
	return ok
}

func ResponseInputToResponseItem(callID string, output string) ResponseItem {
	return ResponseItem{
		Kind:            ResponseOther,
		ID:              callID,
		WebSearchAction: "function_call_output",
		ImageResult:     output,
	}
}
