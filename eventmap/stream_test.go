package eventmap

import (
	"testing"
)

func TestAssistantMarkupAndDeferral(t *testing.T) {
	item := &ResponseItem{Kind: ResponseMessage, Role: "assistant", Content: []ContentItem{{Kind: ContentOutputText, Text: "hello<proposed_plan>secret</proposed_plan>"}}}
	text, ok := LastAssistantMessageFromItem(item, true)
	if !ok || text != "hello" {
		t.Fatalf("text = %q/%v", text, ok)
	}
	if !CompletedItemDefersMailboxDeliveryToNextTurn(item, true) {
		t.Fatalf("expected deferral")
	}
	// Rust #51249: commentary and partial_answer fragments keep mailbox
	// delivery open, so they must not defer it to the next turn.
	item.Phase = "commentary"
	if CompletedItemDefersMailboxDeliveryToNextTurn(item, true) {
		t.Fatalf("commentary should not defer")
	}
	item.Phase = "partial_answer"
	if CompletedItemDefersMailboxDeliveryToNextTurn(item, true) {
		t.Fatalf("partial_answer should not defer")
	}
	item.Phase = "final_answer"
	if !CompletedItemDefersMailboxDeliveryToNextTurn(item, true) {
		t.Fatalf("final_answer should defer")
	}
	// Rust's match arm only defers assistant messages: a reasoning item and an
	// image generation item fall through to `_ => false`.
	// Rust #56133: an explicit final_answer with empty or whitespace-only text is
	// still terminal, while an untagged empty message is not.
	emptyFinal := &ResponseItem{Kind: ResponseMessage, Role: "assistant", Phase: "final_answer"}
	if !CompletedItemDefersMailboxDeliveryToNextTurn(emptyFinal, true) {
		t.Fatalf("empty final_answer should defer")
	}
	whitespaceFinal := &ResponseItem{Kind: ResponseMessage, Role: "assistant", Phase: "final_answer", Content: []ContentItem{{Kind: ContentOutputText, Text: " \n\t"}}}
	if !CompletedItemDefersMailboxDeliveryToNextTurn(whitespaceFinal, true) {
		t.Fatalf("whitespace final_answer should defer")
	}
	emptyUntagged := &ResponseItem{Kind: ResponseMessage, Role: "assistant"}
	if CompletedItemDefersMailboxDeliveryToNextTurn(emptyUntagged, true) {
		t.Fatalf("untagged empty message should not defer")
	}
	item.Phase = ""
	if CompletedItemDefersMailboxDeliveryToNextTurn(&ResponseItem{Kind: ResponseReasoning}, true) {
		t.Fatalf("reasoning should not defer")
	}
	if CompletedItemDefersMailboxDeliveryToNextTurn(&ResponseItem{Kind: ResponseImageGeneration, ID: "img-1"}, true) {
		t.Fatalf("image generation should not defer")
	}
	if CompletedItemDefersMailboxDeliveryToNextTurn(&ResponseItem{Kind: ResponseMessage, Role: "user", Content: []ContentItem{{Kind: ContentOutputText, Text: "hi"}}}, true) {
		t.Fatalf("user message should not defer")
	}
}
