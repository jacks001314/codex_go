package tool

import (
	"encoding/json"
)

// DedupeMCPTextFallback omits the content field of an MCP-shaped result when its
// only content block is a plain text fallback whose JSON equals
// structuredContent (Rust #63540). The JavaScript value keeps both fields; only
// the printed JSON drops the duplicate. Unsuccessful results, other content
// blocks, and differing fallback text are returned unchanged.
func DedupeMCPTextFallback(exported any) (any, bool) {
	object, ok := exported.(map[string]any)
	if !ok {
		return exported, false
	}
	if success, ok := object["success"].(bool); !ok || !success {
		return exported, false
	}
	structured, ok := object["structuredContent"].(map[string]any)
	if !ok {
		return exported, false
	}
	content, ok := object["content"].([]any)
	if !ok || len(content) != 1 {
		return exported, false
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "text" {
		return exported, false
	}
	text, ok := block["text"].(string)
	if !ok {
		return exported, false
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return exported, false
	}
	// Compare canonical JSON so an integer structuredContent value matches the
	// same number parsed from the text fallback.
	fallbackJSON, err := json.Marshal(parsed)
	if err != nil {
		return exported, false
	}
	structuredJSON, err := json.Marshal(structured)
	if err != nil || string(fallbackJSON) != string(structuredJSON) {
		return exported, false
	}
	copied := make(map[string]any, len(object))
	for key, value := range object {
		if key == "content" {
			continue
		}
		copied[key] = value
	}
	return copied, true
}
