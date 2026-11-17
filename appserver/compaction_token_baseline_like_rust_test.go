package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/compact"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Rust keeps TurnItem display projections out of ContextManager. Go stores them
// beside the response items, so compaction estimates must use the same
// model-visible view that history replay sends.
func TestCompactItemsFromSessionItemsExcludesDisplayProjections(t *testing.T) {
	now := fixedTime()
	items := []session.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "hello", CreatedAt: now},
		{ID: "cmd", Type: "commandExecution", Text: strings.Repeat("display output line\n", 1_000), CreatedAt: now},
		{ID: "patch", Type: "fileChange", Text: strings.Repeat("display diff line\n", 1_000), CreatedAt: now},
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: now},
	}

	got := compactItemsFromSessionItems(items)
	if len(got) != 2 || got[0].ID != "u1" || got[1].ID != "a1" {
		t.Fatalf("compactItemsFromSessionItems() = %#v, want only the two response items", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal compact items: %v", err)
	}
	if strings.Contains(string(encoded), "display output line") || strings.Contains(string(encoded), "display diff line") {
		t.Fatalf("display projections leaked into the compaction estimate: %.200s", encoded)
	}
}

// The persisted usage boundary indexes the model-visible history. Counting
// Record.Items would put it past the end after a compaction filters the
// display projections, leaving the next turn unable to estimate the tail.
func TestPersistCompactTokenStatusBoundaryCountsModelVisibleItemsLikeRust(t *testing.T) {
	now := fixedTime()
	store := session.NewStore(t.TempDir())
	const threadID = "thread-visible-usage-boundary"
	record := &session.Record{
		ID: threadID, SessionID: threadID,
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Model: "gpt-5.4"},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "hello", CreatedAt: now},
			{ID: "cmd", Type: "commandExecution", Text: strings.Repeat("display output", 100), CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "answer", CreatedAt: now},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save record error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	usage := model.AgentUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}
	if _, _, err := router.persistCompactTokenStatus(threadID, "gpt-5.4", &turn.TurnStartParams{}, usage, usage); err != nil {
		t.Fatalf("persistCompactTokenStatus() error = %v", err)
	}
	stored, err := store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}
	visible := compactItemsFromSessionItems(stored.Items)
	if len(visible) == len(stored.Items) {
		t.Fatalf("test record must contain a filtered display projection")
	}
	if boundary := tokenUsageBoundaryFromMetadata(stored.Metadata.Extra); boundary != len(visible) {
		t.Fatalf("token_usage_boundary = %d, want the %d model-visible items", boundary, len(visible))
	}
}

// Rust `Session::recompute_token_usage` replaces the pre-compaction baseline
// after `replace_compacted_history`. Without this, a persisted
// `contextWindowExceeded` marker keeps compacting every turn.
func TestRecomputeCompactedTokenUsageClearsStaleOverflowBaselineLikeRust(t *testing.T) {
	now := fixedTime()
	store := session.NewStore(t.TempDir())
	const threadID = "thread-compacted-baseline"
	record := &session.Record{
		ID: threadID, SessionID: threadID,
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{
			Model: "gpt-5.4",
			Extra: map[string]any{
				"last_token_usage":          map[string]any{"totalTokens": int64(259_000)},
				"total_token_usage":         map[string]any{"totalTokens": int64(900_000)},
				"token_usage_boundary":      int64(176),
				"server_reasoning_included": false,
				"model_context_window":      int64(258_400),
				"token_status": compactTokenStatusMap(compact.TokenStatus{
					ActiveContextTokens:      259_000,
					ShouldCompact:            true,
					Reason:                   compact.ReasonContextWindowExceeded,
					NewContextWindowRequired: true,
				}),
			},
		},
		Items: []session.Item{
			{ID: "summary", Type: "message", Role: "user", Text: "compaction summary", CreatedAt: now},
			{ID: "next", Type: "message", Role: "user", Text: "continue the work", CreatedAt: now},
			{ID: "cmd", Type: "commandExecution", Text: strings.Repeat("x", 400_000), CreatedAt: now},
			{ID: "checkpoint", Type: "contextCompaction", CreatedAt: now},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save record error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	params := &turn.TurnStartParams{Config: map[string]any{"model_auto_compact_token_limit": 244_800}}

	status := router.recomputeCompactedTokenUsage(record, record.Metadata.Extra, "gpt-5.4", params, "")
	if status.ShouldCompact {
		t.Fatalf("recomputed status still compacts: %#v", status)
	}
	visible := compactItemsFromSessionItems(record.Items)
	wantTokens := compact.EstimateTokens(visible)
	if boundary := tokenUsageBoundaryFromMetadata(record.Metadata.Extra); boundary != len(visible) {
		t.Fatalf("token_usage_boundary = %d, want the %d model-visible items", boundary, len(visible))
	}
	usage, _ := record.Metadata.Extra["last_token_usage"].(map[string]any)
	if got := intFromAny(firstMapValue(usage, "totalTokens", "total_tokens")); got != wantTokens {
		t.Fatalf("recomputed last_token_usage = %d, want the compacted estimate %d", got, wantTokens)
	}
	if !tokenUsageIncludesReasoning(record.Metadata.Extra) {
		t.Fatalf("the local estimate must mark retained reasoning as included")
	}
	if err := router.runtimeSaveThreadRecord(record); err != nil {
		t.Fatalf("runtimeSaveThreadRecord() error = %v", err)
	}
	if next := router.compactTokenStatusForTurn(threadID, "gpt-5.4", params); next.ShouldCompact {
		t.Fatalf("the next pre-turn check compacts again: %#v", next)
	}
}

// Rust suppresses the base-instruction estimate once the window records tool
// declarations or a `model.base_instructions` fragment.
func TestRecomputeCompactedTokenUsageAvoidsDoubleCountingRecordedInstructionsLikeRust(t *testing.T) {
	now := fixedTime()
	baseInstructions := strings.Repeat("base instruction line\n", 2_000)
	baseTokens := compact.EstimateTextTokens(baseInstructions)
	if baseTokens <= 0 {
		t.Fatal("test base instructions must have a positive estimate")
	}
	params := &turn.TurnStartParams{Config: map[string]any{"model_auto_compact_token_limit": 244_800}}
	router := NewRuntimeRouter(RuntimeServices{})

	message := session.Item{ID: "u1", Type: "message", Role: "user", Text: "hello", CreatedAt: now}
	record := &session.Record{ID: "thread-no-declarations", Items: []session.Item{message}}
	items := compactItemsFromSessionItems(record.Items)
	status := router.recomputeCompactedTokenUsage(record, map[string]any{}, "gpt-5.4", params, baseInstructions)
	if want := compact.EstimateTokens(items) + baseTokens; status.ActiveContextTokens != want {
		t.Fatalf("active context = %d, want items plus base instructions %d", status.ActiveContextTokens, want)
	}

	catalog := session.Item{
		ID: "at1", Type: "additional_tools", Role: "developer", CreatedAt: now,
		Raw: json.RawMessage(`{"type":"additional_tools","role":"developer","tools":[{"type":"function","name":"echo"}]}`),
	}
	instructions := session.Item{
		ID: "base", Type: "message", Role: "developer", Text: baseInstructions, CreatedAt: now,
		Data: map[string]any{"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []any{session.BaseInstructionsContentKind},
		}},
	}
	record = &session.Record{ID: "thread-recorded-declarations", Items: []session.Item{catalog, instructions, message}}
	items = compactItemsFromSessionItems(record.Items)
	status = router.recomputeCompactedTokenUsage(record, map[string]any{}, "gpt-5.4", params, baseInstructions)
	if want := compact.EstimateTokens(items); status.ActiveContextTokens != want {
		t.Fatalf("active context = %d, want only the recorded history %d", status.ActiveContextTokens, want)
	}
}

// Rust's `is_api_message` keeps a configuration update only when the
// harness authored it. Replay already follows that rule; the compaction
// estimate must charge the same items or the model can receive context the
// budget never accounted for.
func TestCompactItemsKeepHarnessConfigurationUpdatesLikeRust(t *testing.T) {
	now := fixedTime()
	items := []session.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "hello", CreatedAt: now},
		{ID: "trusted", Type: "configuration_update", CreatedAt: now, Data: map[string]any{
			"reasoning":        map[string]any{"effort": "high"},
			"harness_metadata": map[string]any{"harness_authored_configuration": true},
		}},
		{ID: "untrusted", Type: "configuration_update", CreatedAt: now, Data: map[string]any{
			"reasoning": map[string]any{"effort": "low"},
		}},
		{ID: "cmd", Type: "commandExecution", Text: "display output", CreatedAt: now},
	}
	got := compactItemsFromSessionItems(items)
	if len(got) != 2 || got[0].ID != "u1" || got[1].ID != "trusted" {
		t.Fatalf("compactItemsFromSessionItems() = %#v, want the user message and trusted update", got)
	}
}
