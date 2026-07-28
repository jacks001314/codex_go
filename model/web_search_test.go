package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHostedWebSearchResponseItemPreservesAction(t *testing.T) {
	item, ok := toolCallAgentItem(&responsesAgentOutputItem{
		ID:     "search-1",
		Type:   "web_search_call",
		Status: "completed",
		Action: map[string]any{"type": "search", "query": "codex"},
	}, 0)
	if !ok || item.Type != "web_search_call" || item.Search["query"] != "codex" {
		t.Fatalf("item = %#v, ok = %v", item, ok)
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"action":{"query":"codex","type":"search"}`) {
		t.Fatalf("item json = %s", data)
	}
}
