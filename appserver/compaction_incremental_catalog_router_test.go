package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/rollout"
	"codex_go/session"
)

// TestRouterThreadCompactStartPlacesToolCatalogBeforeSummaryLikeRust drives the
// legacy `Router` compaction entry point - the in-process `ThreadRouter` path,
// `Router.Handle` -> `dispatch` -> `handleThreadCompactStart` - rather than the
// RuntimeRouter path that TestCompactionPlacesToolCatalogBeforeSummaryLikeRust
// exercises.
//
// Rust #51188 (`062439b2f8`, "Record base instructions in incremental tool
// history") has a single compaction entry, so both Go entry points must place
// the window's recorded declaration prefix ahead of the rebuilt history, ahead
// of the window's first user message and the compaction summary
// (`codex-rs/core/src/compact.rs::assemble_compaction_history`, compared in
// `assemble_compaction_history_keeps_prefix_first_and_summary_last`).
//
// RuntimeRouter intercepts `thread/compact/start` (`MethodThreadCompactStart`
// is inside `isThreadMethod`), so `Router.handleThreadCompactStart` is a
// production-unreachable legacy path; this test is what keeps its
// `placeIncrementalCatalogBeforeCompactedHistory` call honest. Removing that
// call makes this test FAIL with `replacement_history[0] = "message"`.
func TestRouterThreadCompactStartPlacesToolCatalogBeforeSummaryLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	const threadID = "thread-router-catalog-order"
	now := fixedTime()
	instructions := "Use the available tools to help the user."
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())

	if err := store.Save(&session.Record{
		ID: threadID, SessionID: threadID, CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", Extra: map[string]any{}},
		Items: append(declarationPrefixSessionItemsLikeRust(t, definitions, instructions, threadID, now),
			session.Item{ID: "u1", Type: "message", Role: "user", Text: "first request", CreatedAt: now},
			session.Item{ID: "a1", Type: "agent_message", Role: "assistant", Text: "first answer", CreatedAt: now},
		),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	router := NewRouter(store)
	if err := router.createThreadRollout(&session.Record{ID: threadID, SessionID: threadID}, now); err != nil {
		t.Fatalf("create rollout error: %v", err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: threadID}))
	if response.Error != nil {
		t.Fatalf("compact error: %+v", response.Error)
	}
	path, err := rollout.FindThreadPath(store.Root(), threadID, false)
	if err != nil {
		t.Fatalf("rollout path error: %v", err)
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	payload := lastCompactedCheckpoint(t, lines)
	var envelope struct {
		ReplacementHistory []json.RawMessage `json:"replacement_history"`
	}
	if err := json.Unmarshal(payload.Raw, &envelope); err != nil {
		t.Fatalf("decode replacement_history: %v", err)
	}
	if len(envelope.ReplacementHistory) < 2 {
		t.Fatalf("replacement_history = %d items, want the rebuilt history", len(envelope.ReplacementHistory))
	}
	if got := replacementHistoryItemType(t, envelope.ReplacementHistory[0]); got != "additional_tools" {
		t.Fatalf("replacement_history[0] = %q, want the tool catalog ahead of the rebuilt history", got)
	}
	if got := replacementHistoryItemType(t, envelope.ReplacementHistory[1]); got != "message" {
		t.Fatalf("replacement_history[1] = %q, want the base-instruction developer message", got)
	}
	if got := replacementHistoryItemRole(t, envelope.ReplacementHistory[1]); got != "developer" {
		t.Fatalf("replacement_history[1] role = %q, want developer", got)
	}

	// Cold replay reads the same order: the catalog ahead of the compaction
	// summary and ahead of the window's first user message.
	cold, err := rollout.RecordFromPath(path, false)
	if err != nil {
		t.Fatalf("RecordFromPath() error = %v", err)
	}
	catalogIndex, summaryIndex, firstUserIndex := -1, -1, -1
	for i := range cold.Items {
		if cold.Items[i].Type == "additional_tools" && catalogIndex < 0 {
			catalogIndex = i
		}
		if cold.Items[i].Type == "message" && cold.Items[i].Role == "user" {
			switch {
			case strings.Contains(cold.Items[i].Text, "compaction summary") || strings.Contains(cold.Items[i].Text, "Another language model started"):
				if summaryIndex < 0 {
					summaryIndex = i
				}
			case cold.Items[i].Text == "first request":
				if firstUserIndex < 0 {
					firstUserIndex = i
				}
			}
		}
	}
	if catalogIndex < 0 {
		t.Fatalf("cold replay lost the tool catalog: %#v", cold.Items)
	}
	if summaryIndex < 0 {
		t.Fatalf("cold replay lost the compaction summary: %#v", cold.Items)
	}
	if firstUserIndex < 0 {
		t.Fatalf("cold replay lost the first user message: %#v", cold.Items)
	}
	if catalogIndex > summaryIndex {
		t.Fatalf("catalog index %d is after the summary index %d in the rebuilt history", catalogIndex, summaryIndex)
	}
	if catalogIndex > firstUserIndex {
		t.Fatalf("catalog index %d is after the window's first user message index %d", catalogIndex, firstUserIndex)
	}
}
