package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

// TestCompactionPlacesToolCatalogBeforeSummaryLikeRust covers the position
// semantics of Rust #51188 (`062439b2f8`, "Record base instructions in
// incremental tool history"),
// `codex-rs/core/src/compact.rs::assemble_compaction_history`, compared in
// `assemble_compaction_history_keeps_prefix_first_and_summary_last` and
// `assemble_compaction_history_keeps_compaction_last`.
//
// A compaction rebuilds the context window's history. The window prefix - the
// `additional_tools` catalog and the base-instruction developer message -
// must open that rebuilt history, ahead of the window's first user message and
// the compaction summary, which stays last. Go drives the real compaction
// write-back (`thread/compact/start` -> `RuntimeRouter.compactThreadWithHistory`
// -> `appendRuntimeCompacted`), reads the checkpoint back from the rollout and
// cold-replays it, so the assertion covers what a resumed thread actually sees.
func TestCompactionPlacesToolCatalogBeforeSummaryLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	const threadID = "thread-compaction-catalog-order"
	created := time.Now().UTC()
	instructions := "Use the available tools to help the user."
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())

	if err := store.Save(&session.Record{
		ID: threadID, SessionID: threadID, CreatedAt: created, UpdatedAt: created, RecencyAt: created,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", Extra: map[string]any{}},
		Items: append(declarationPrefixSessionItemsLikeRust(t, definitions, instructions, threadID, created),
			session.Item{ID: "u1", Type: "message", Role: "user", Text: "first request", CreatedAt: created},
			session.Item{ID: "a1", Type: "agent_message", Role: "assistant", Text: "first answer", CreatedAt: created},
		),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(NewNotificationBuffer())
	router.requireThreadStatus().UpsertThread(threadID, false)

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
	// The persisted checkpoint opens with the catalog and the base-instruction
	// developer message.
	if len(envelope.ReplacementHistory) < 4 {
		t.Fatalf("replacement_history = %d items, want the prefix plus the rebuilt history", len(envelope.ReplacementHistory))
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

	// The replacement window replays the recorded declarations instead of
	// re-declaring them, while a later declaration change still travels as an
	// incremental update.
	history := incrementalHistoryItems(t, router, threadID)
	replay, err := router.incrementalToolCatalogForTurn(threadID, history, definitions, instructions, "turn-2", created.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Active || replay.Prefix || len(replay.Items) != 0 || len(replay.SessionItems) != 0 {
		t.Fatalf("replacement window turn = %#v, want a replay without updates", replay)
	}
	changed := incrementalCatalogLikeRust("Updated execution instructions.", incrementalToolSearchLikeRust())
	update, err := router.incrementalToolCatalogForTurn(threadID, history, changed, instructions, "turn-3", created.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !update.Active || update.Prefix || len(update.SessionItems) == 0 {
		t.Fatalf("changed catalog turn = %#v, want an incremental update", update)
	}
}

func declarationPrefixSessionItemsLikeRust(t *testing.T, tools []any, instructions string, threadID string, createdAt time.Time) []session.Item {
	t.Helper()
	items := []any{
		map[string]any{"type": "additional_tools", "role": "developer", "tools": tools},
		map[string]any{
			"type": "message", "role": "developer",
			"content": []map[string]any{{"type": "input_text", "text": instructions}},
		},
	}
	out := make([]session.Item, 0, len(items))
	for i := range items {
		item, ok := incrementalToolCatalogSessionItem("turn-1", i, items[i], createdAt)
		if !ok {
			t.Fatalf("prefix item %d (%v) is not recordable", i, items[i])
		}
		out = append(out, item)
	}
	return out
}

func replacementHistoryItemType(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode replacement item: %v", err)
	}
	value, _ := object["type"].(string)
	return strings.TrimSpace(value)
}

func replacementHistoryItemRole(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode replacement item: %v", err)
	}
	value, _ := object["role"].(string)
	return strings.TrimSpace(value)
}
