package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// Rust #62589 stops checkpointing cumulative MCP attribution into rollout
// items: recorded items carry only the remaining harness metadata, and search
// ownership comes from model_search_state.
func TestRecordedItemsCarryNoMcpAttributionLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-mcp-attribution-removed")
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy)},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })

	firstTurnID := "turn_1"
	item := session.Item{
		ID: "msg-1", Type: "message", Role: "user", Text: "hello", CreatedAt: now,
		Data: map[string]any{"turnId": firstTurnID},
	}
	record, err := router.runtimeAppendItems(threadID, []session.Item{item})
	if err != nil {
		t.Fatalf("runtimeAppendItems() error = %v", err)
	}
	if record == nil || len(record.Items) == 0 {
		t.Fatalf("recorded items = %#v", record)
	}
	for i := range record.Items {
		raw := harnessMetadataRawFromItem(&record.Items[i])
		if strings.Contains(string(raw), "mcp_attribution") {
			t.Fatalf("recorded item %d carries mcp_attribution: %s", i, raw)
		}
	}

	// New rollouts keep the provider history in model_search_state.
	if !router.claimModelSearchProvider(string(threadID), modelSearchClientMCP) {
		t.Fatal("failed to claim Search MCP")
	}
	searchItems := []session.Item{{ID: "search", Type: "web_search_call"}}
	router.annotateModelSearchMetadata(string(threadID), searchItems)
	raw := harnessMetadataRawFromItem(&searchItems[0])
	if !strings.Contains(string(raw), "model_search_state") {
		t.Fatalf("search checkpoint = %s, want model_search_state", raw)
	}
	if strings.Contains(string(raw), "mcp_attribution") {
		t.Fatalf("search checkpoint kept mcp_attribution: %s", raw)
	}
}
