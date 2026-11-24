package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/turn"
)

// Rust #63474: the encrypted SessionStart context is persisted as a tool output
// whose payload is the encrypted content item, so a resumed thread replays the
// ciphertext instead of dropping it or turning it into text.
func TestHookEncryptedContextSessionItemsLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	items := hookEncryptedContextSessionItems("turn-1", []turn.HookEncryptedContext{
		{Name: "encrypted_output", Content: "gAAAA-test"},
		{Name: "dropped", Content: ""},
	}, now)
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item := items[0]
	if item.Type != "function_call_output" || item.Name != "encrypted_output" {
		t.Fatalf("item = %#v", item)
	}
	if item.Text != "" {
		t.Fatalf("plaintext text = %q, want empty", item.Text)
	}
	contentItems, ok := item.Data["content_items"].([]map[string]any)
	if !ok || len(contentItems) != 1 || contentItems[0]["type"] != "encrypted_content" || contentItems[0]["encrypted_content"] != "gAAAA-test" {
		t.Fatalf("content items = %#v", item.Data["content_items"])
	}
	var raw map[string]any
	if err := json.Unmarshal(item.Raw, &raw); err != nil {
		t.Fatalf("raw = %v", err)
	}
	output, ok := raw["output"].([]any)
	if !ok || len(output) != 1 {
		t.Fatalf("raw output = %#v", raw["output"])
	}
	if raw["name"] != "encrypted_output" || raw["type"] != "function_call_output" {
		t.Fatalf("raw item = %#v", raw)
	}
}
