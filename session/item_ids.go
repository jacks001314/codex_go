package session

import "strings"

// responseItemIDPrefixes mirrors Rust's `ResponseItem::id_prefix` (#59312): the
// prefix of the Responses item id assigned to an item that carries none.
var responseItemIDPrefixes = map[string]string{
	"additional_tools":        "at",
	"message":                 "msg",
	"user_message":            "msg",
	"agent_message":           "amsg",
	"assistant_message":       "amsg",
	"reasoning":               "rs",
	"local_shell_call":        "lsh",
	"function_call":           "fc",
	"tool_search_call":        "tsc",
	"function_call_output":    "fco",
	"custom_tool_call":        "ctc",
	"custom_tool_call_output": "ctco",
	"tool_search_output":      "tso",
	"web_search_call":         "ws",
	"image_generation_call":   "ig",
	"compaction":              "cmp",
	"context_compaction":      "cmp",
}

// messageItemTypes keep their Responses id on ResponseItemID; every other
// response item carries it in ID.
func messageItemType(itemType string) bool {
	switch itemType {
	case "message", "user_message", "agent_message", "assistant_message":
		return true
	default:
		return false
	}
}

// BackfillMissingItemIDs assigns a generated id to every response item that has
// none, preserving ids that are already present (Rust #59312). Message items
// store the id on ResponseItemID, which is what history replay reports to the
// model; other response items store it on ID. Items kept as a raw payload are
// left alone, and thread-item types without a Responses prefix are skipped.
// It returns the number of items that received an id.
func BackfillMissingItemIDs(items []Item, generate func(prefix string) string) int {
	if generate == nil {
		return 0
	}
	assigned := 0
	for i := range items {
		itemType := strings.ToLower(strings.TrimSpace(items[i].Type))
		prefix, ok := responseItemIDPrefixes[itemType]
		if !ok || len(items[i].Raw) > 0 {
			continue
		}
		if messageItemType(itemType) {
			if strings.TrimSpace(items[i].ResponseItemID) == "" {
				items[i].ResponseItemID = generate(prefix)
				assigned++
			}
			continue
		}
		if strings.TrimSpace(items[i].ID) == "" {
			items[i].ID = generate(prefix)
			assigned++
		}
	}
	return assigned
}
