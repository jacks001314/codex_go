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
	item.Phase = "commentary"
	if CompletedItemDefersMailboxDeliveryToNextTurn(item, true) {
		t.Fatalf("commentary should not defer")
	}
}
