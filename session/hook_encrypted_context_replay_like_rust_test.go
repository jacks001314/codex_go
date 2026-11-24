package session

import (
	"encoding/json"
	"testing"
)

// Rust #63474: replaying a recorded encrypted hook output keeps the encrypted
// content item and never substitutes plaintext tool output.
func TestHistoryReplaysEncryptedHookToolOutputLikeRust(t *testing.T) {
	contentItems := []map[string]any{{"type": "encrypted_content", "encrypted_content": "gAAAA-test"}}
	payload, _ := json.Marshal(map[string]any{
		"type":   "function_call_output",
		"name":   "encrypted_output",
		"output": contentItems,
	})
	record := &Record{Items: []Item{{
		ID:   "hook-encrypted-context-turn-1-1",
		Type: "function_call_output",
		Name: "encrypted_output",
		Data: map[string]any{
			"name":          "encrypted_output",
			"output":        "",
			"content_items": contentItems,
			"harness_metadata": map[string]any{
				"hook_encrypted_context": true,
			},
		},
		Raw: payload,
	}}}

	items := InputItemsFromRecord(record, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["type"] != "function_call_output" || item["name"] != "encrypted_output" {
		t.Fatalf("item = %#v", items[0])
	}
	if text, ok := item["output"].(string); ok {
		t.Fatalf("output degraded to plaintext: %q", text)
	}
	normalized, err := json.Marshal(item["output"])
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	var output []map[string]any
	if err := json.Unmarshal(normalized, &output); err != nil {
		t.Fatalf("output is not a content item array: %v (%s)", err, normalized)
	}
	if len(output) != 1 || output[0]["type"] != "encrypted_content" || output[0]["encrypted_content"] != "gAAAA-test" {
		t.Fatalf("output = %s", normalized)
	}
}
