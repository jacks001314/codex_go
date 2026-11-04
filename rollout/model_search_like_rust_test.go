package rollout

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/session"
)

func TestModelSearchCheckpointPersistsNativeOwnershipLikeRust(t *testing.T) {
	item := session.Item{
		ID:        "search-1",
		Type:      "webSearch",
		CreatedAt: time.Unix(1, 0),
		Data: map[string]any{
			"action":           map[string]any{"type": "search", "query": "paper"},
			"results":          []any{map[string]any{"title": "paper"}},
			"harness_metadata": json.RawMessage(`{"native_search_fallback_used":true}`),
		},
	}
	payload, metadata, ok := modelSearchCheckpointRolloutItem(&item)
	if !ok {
		t.Fatal("native search item was not selected for a response_item checkpoint")
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if decoded["type"] != "web_search_call" || decoded["id"] != "search-1" || decoded["status"] != "completed" {
		t.Fatalf("payload = %#v", decoded)
	}
	var fields map[string]any
	if err := json.Unmarshal(metadata, &fields); err != nil || fields["native_search_fallback_used"] != true {
		t.Fatalf("metadata = %s", metadata)
	}
}

func TestModelSearchCheckpointIgnoresUnmarkedOrNonSearchItemsLikeRust(t *testing.T) {
	for _, item := range []session.Item{
		{ID: "search-2", Type: "webSearch", Data: map[string]any{}},
		{ID: "message-1", Type: "agent_message", Data: map[string]any{"harness_metadata": json.RawMessage(`{"native_search_fallback_used":true}`)}},
	} {
		if _, _, ok := modelSearchCheckpointRolloutItem(&item); ok {
			t.Fatalf("item %#v unexpectedly produced a search checkpoint", item)
		}
	}
}
