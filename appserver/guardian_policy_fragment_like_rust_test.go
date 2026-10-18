package appserver

import (
	"strings"

	"codex_go/eventmap"
)

func guardianPolicyInputText(items []any) string {
	for index := len(items) - 1; index >= 0; index-- {
		item, ok := items[index].(map[string]any)
		if !ok || strings.TrimSpace(stringFromAny(item["role"])) != "developer" {
			continue
		}
		metadata, _ := item["internal_chat_message_metadata_passthrough"].(map[string]any)
		if metadata == nil {
			continue
		}
		kinds := stringSliceFromAny(metadata["content_item_kinds"])
		if len(kinds) != 1 || kinds[0] != "guardian.policy" {
			continue
		}
		var text strings.Builder
		appendPart := func(part map[string]any) {
			if stringFromAny(part["type"]) == string(eventmap.ContentInputText) {
				text.WriteString(stringFromAny(part["text"]))
			}
		}
		switch content := item["content"].(type) {
		case []any:
			for _, raw := range content {
				part, _ := raw.(map[string]any)
				appendPart(part)
			}
		case []map[string]any:
			for _, part := range content {
				appendPart(part)
			}
		}
		return text.String()
	}
	return ""
}
