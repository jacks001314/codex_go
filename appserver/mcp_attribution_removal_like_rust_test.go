package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// Rust #62589 removed cumulative MCP attribution and #62808 removed the
// model-selected Search MCP provider checkpoint, so a recorded item carries no
// search-ownership member in its trusted harness metadata.
func TestRecordedItemsCarryNoSearchOwnershipMetadataLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-model-search-removed")
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy)},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })

	item := session.Item{
		ID: "msg-1", Type: "message", Role: "user", Text: "hello", CreatedAt: now,
		Data: map[string]any{"turnId": "turn_1"},
	}
	record, err := router.runtimeAppendItems(threadID, []session.Item{item})
	if err != nil {
		t.Fatalf("runtimeAppendItems() error = %v", err)
	}
	if record == nil || len(record.Items) == 0 {
		t.Fatalf("recorded items = %#v", record)
	}
	for i := range record.Items {
		raw := string(harnessMetadataRawFromItem(&record.Items[i]))
		for _, removed := range []string{"mcp_attribution", "model_search_state", "native_search_fallback_used"} {
			if strings.Contains(raw, removed) {
				t.Fatalf("recorded item %d carries removed search metadata %q: %s", i, removed, raw)
			}
		}
	}
}
