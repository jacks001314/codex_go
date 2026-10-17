package session

import "testing"

// TestSanitizeHistoryInputItemDropsHarnessMetadataLikeRust mirrors Rust #52671:
// retention annotations (harness metadata) mark storage provenance and never
// appear in model requests, while model-visible content kinds survive.
func TestSanitizeHistoryInputItemDropsHarnessMetadataLikeRust(t *testing.T) {
	item := map[string]any{
		"type":             "function_call_output",
		"name":             "inspect",
		"output":           "done",
		"harness_metadata": map[string]any{"client_authored": true},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []any{"notes.thread_hint"},
			"retain":             true,
		},
	}
	sanitized, _ := sanitizeHistoryInputItem(item).(map[string]any)
	if sanitized == nil {
		t.Fatal("sanitized item is nil")
	}
	if _, present := sanitized["harness_metadata"]; present {
		t.Fatalf("sanitized item kept harness metadata: %#v", sanitized)
	}
	passthrough, _ := sanitized["internal_chat_message_metadata_passthrough"].(map[string]any)
	if _, present := passthrough["retain"]; present {
		t.Fatalf("sanitized item kept the retention annotation: %#v", passthrough)
	}
	if _, ok := passthrough["content_item_kinds"]; !ok {
		t.Fatalf("sanitized item dropped content kinds: %#v", passthrough)
	}
}
