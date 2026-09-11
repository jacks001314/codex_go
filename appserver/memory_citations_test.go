package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/state"
)

const memoryCitationText = "Here is the answer.\n" +
	"<oai-mem-citation>\n" +
	"<citation_entries>\n" +
	"rollouts/a.jsonl:1-2|note=[used prior run]\n" +
	"</citation_entries>\n" +
	"<rollout_ids>\n" +
	"thread-a\n" +
	"</rollout_ids>\n" +
	"</oai-mem-citation>"

func newMemoryCitationTestRouter(t *testing.T, extra map[string]any) (*RuntimeRouter, *state.StateRuntime, string) {
	t.Helper()
	home := t.TempDir()
	ctx := context.Background()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, sqliteConfig, "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateRuntime.Close() })

	now := time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)
	store := session.NewStore(home)
	threadRouter := NewRouter(store)
	threadRouter.SetStateRuntime(stateRuntime)
	threadID := "memory-citation-thread"
	record := &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: "memory-citation-session",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           home,
			ModelProvider: "openai",
			HistoryMode:   "paginated",
			Extra:         cloneAnyMap(extra),
		},
		Items: []session.Item{{
			ID:       "u1",
			Type:     "message",
			Role:     "user",
			Text:     "hello",
			Metadata: map[string]any{"turnId": "turn-1"},
		}},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, StateRuntime: stateRuntime})
	return router, stateRuntime, threadID
}

func insertStage1UsageRow(t *testing.T, runtime *state.StateRuntime, storeVersion string, threadID string) *state.StateRuntime {
	t.Helper()
	store, err := runtime.MemoryStoreForVersion(context.Background(), storeVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MemoriesDB().Exec(`
INSERT INTO stage1_outputs (thread_id, source_updated_at, raw_memory, rollout_summary, generated_at, usage_count)
VALUES (?, ?, ?, ?, ?, ?)`, threadID, 1, "raw", "summary", 1, 0); err != nil {
		t.Fatal(err)
	}
	return store
}

func stage1UsageCount(t *testing.T, store *state.StateRuntime, threadID string) int64 {
	t.Helper()
	var usage int64
	if err := store.MemoriesDB().QueryRow(`SELECT usage_count FROM stage1_outputs WHERE thread_id = ?`, threadID).Scan(&usage); err != nil {
		t.Fatal(err)
	}
	return usage
}

// TestRecordMemoryCitationUsageRecordsCitedThreads mirrors Rust
// record_completed_response_item_with_finalized_facts: a completed assistant
// message that cites stage-1 memory outputs increments usage for each cited
// thread against the selected version's store.
func TestRecordMemoryCitationUsageRecordsCitedThreads(t *testing.T) {
	router, stateRuntime, threadID := newMemoryCitationTestRouter(t, nil)
	v1Store := insertStage1UsageRow(t, stateRuntime, "v1", "thread-a")

	if !router.recordMemoryCitationUsage(threadID, []session.Item{
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: memoryCitationText},
	}) {
		t.Fatal("recordMemoryCitationUsage() = false, want true")
	}
	if got := stage1UsageCount(t, v1Store, "thread-a"); got != 1 {
		t.Fatalf("v1 usage_count = %d, want 1", got)
	}
}

// TestRecordMemoryCitationUsageUsesSelectedVersion proves the usage write is
// routed to the thread's configured memory version (Rust
// memories_for_version(config.memories.version)).
func TestRecordMemoryCitationUsageUsesSelectedVersion(t *testing.T) {
	router, stateRuntime, threadID := newMemoryCitationTestRouter(t, map[string]any{
		"config": map[string]any{"memories": map[string]any{"version": "v2"}},
	})
	v1Store := insertStage1UsageRow(t, stateRuntime, "v1", "thread-a")
	v2Store := insertStage1UsageRow(t, stateRuntime, "v2", "thread-a")

	if !router.recordMemoryCitationUsage(threadID, []session.Item{
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: memoryCitationText},
	}) {
		t.Fatal("recordMemoryCitationUsage() = false, want true")
	}
	if got := stage1UsageCount(t, v2Store, "thread-a"); got != 1 {
		t.Fatalf("v2 usage_count = %d, want 1", got)
	}
	if got := stage1UsageCount(t, v1Store, "thread-a"); got != 0 {
		t.Fatalf("v1 usage_count = %d, want 0", got)
	}
}

// TestRecordMemoryCitationUsageIgnoresUncitedMessages proves ordinary
// assistant and user items do not touch memory usage.
func TestRecordMemoryCitationUsageIgnoresUncitedMessages(t *testing.T) {
	router, stateRuntime, threadID := newMemoryCitationTestRouter(t, nil)
	v1Store := insertStage1UsageRow(t, stateRuntime, "v1", "thread-a")

	items := []session.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "did you remember?"},
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "yes, but without a citation"},
	}
	if router.recordMemoryCitationUsage(threadID, items) {
		t.Fatal("recordMemoryCitationUsage() = true, want false")
	}
	if got := stage1UsageCount(t, v1Store, "thread-a"); got != 0 {
		t.Fatalf("usage_count = %d, want 0", got)
	}
}

// TestRecordMemoryCitationUsageReportsCitationWithoutUsableIDs mirrors Rust's
// bool contract: a parseable citation with no valid thread IDs counts as a
// citation but records nothing.
func TestRecordMemoryCitationUsageReportsCitationWithoutUsableIDs(t *testing.T) {
	router, stateRuntime, threadID := newMemoryCitationTestRouter(t, nil)
	v1Store := insertStage1UsageRow(t, stateRuntime, "v1", "thread-a")

	text := "<oai-mem-citation><citation_entries>x:1-1|note=[n]</citation_entries></oai-mem-citation>"
	if !router.recordMemoryCitationUsage(threadID, []session.Item{
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: text},
	}) {
		t.Fatal("recordMemoryCitationUsage() = false, want true")
	}
	if got := stage1UsageCount(t, v1Store, "thread-a"); got != 0 {
		t.Fatalf("usage_count = %d, want 0", got)
	}
}
