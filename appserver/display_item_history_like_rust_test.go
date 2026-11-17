package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"codex_go/compact"
	"codex_go/session"
	"codex_go/turn"
)

// Regression for the "Codex ran out of room in the model's context window"
// report: Go records the TUI TurnItem projection of every tool call
// (commandExecution, carrying the full aggregated output) next to the response
// items. Rust never lets those into ContextManager, so before this fix a
// resumed or forked turn replayed megabytes of display text as user messages
// while the token estimate (built from the same record) reported a comfortable
// window. Both views now describe the same model-visible items.
func TestDisplayItemsDoNotInflateResumedContextLikeRust(t *testing.T) {
	const toolCalls = 700
	displayOutput := strings.Repeat("command output line\n", 200)

	store := session.NewStore(t.TempDir())
	now := fixedTime()
	items := []session.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "run the tool", CreatedAt: now},
	}
	for i := 0; i < toolCalls; i++ {
		callID := fmt.Sprintf("call-%d", i)
		items = append(items,
			session.Item{
				ID:        "cmd-" + callID,
				Type:      "commandExecution",
				CallID:    callID,
				Text:      displayOutput,
				CreatedAt: now,
				Data:      map[string]any{"aggregatedOutput": displayOutput},
			},
			session.Item{ID: "call-" + callID, Type: "function_call", CallID: callID, Name: "shell", Text: `{"cmd":"rg"}`, CreatedAt: now},
			session.Item{ID: "out-" + callID, Type: "tool_output", CallID: callID, Text: "ok", CreatedAt: now},
		)
	}
	items = append(items, session.Item{ID: "a1", Type: "agent_message", Role: "assistant", Text: "done", CreatedAt: now})

	const threadID = "thread-display-history"
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: threadID,
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Model: "gpt-5.4"},
		Items:    items,
	}); err != nil {
		t.Fatalf("Create record error = %v", err)
	}

	record, err := store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("Read record error = %v", err)
	}

	// The request must carry exactly the response items: one prompt, one
	// call/output pair per tool call, and the final answer.
	replayed := session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true})
	if want := toolCalls*2 + 2; len(replayed) != want {
		t.Fatalf("replayed history has %d items, want %d", len(replayed), want)
	}
	encoded, err := json.Marshal(replayed)
	if err != nil {
		t.Fatalf("marshal replayed history: %v", err)
	}
	if strings.Contains(string(encoded), "command output line") {
		t.Fatalf("commandExecution display output leaked into the request (%d bytes)", len(encoded))
	}

	// The context estimate must describe the same item set: it may not charge
	// the display transcript, and the replayed request may not contain it either.
	estimated := compact.EstimateTokens(compactItemsFromSessionItems(record.Items))
	if estimated <= 0 {
		t.Fatalf("estimate must be positive: %d", estimated)
	}
	displayTokens := compact.EstimateTextTokens(strings.Repeat(displayOutput, toolCalls))
	if displayTokens <= estimated*10 {
		t.Fatalf("display transcripts (%d tokens) should dwarf the model-visible estimate (%d tokens)", displayTokens, estimated)
	}
	if len(encoded) > len(displayOutput)*toolCalls/4 {
		t.Fatalf("replayed request is %d bytes, which still carries the display transcript", len(encoded))
	}

	// The pre-turn check now measures the real window: a limit below the
	// model-visible history triggers compaction instead of reporting room left.
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	tight := &turn.TurnStartParams{Config: map[string]any{"model_auto_compact_token_limit": estimated / 2}}
	status := router.compactTokenStatusForTurn(threadID, "gpt-5.4", tight)
	if !status.ShouldCompact {
		t.Fatalf("pre-turn check missed the budget: %#v (estimated=%d)", status, estimated)
	}
	if status.ActiveContextTokens != estimated {
		t.Fatalf("active context %d is not the model-visible history estimate %d", status.ActiveContextTokens, estimated)
	}
	roomy := &turn.TurnStartParams{Config: map[string]any{"model_auto_compact_token_limit": estimated * 4}}
	if status := router.compactTokenStatusForTurn(threadID, "gpt-5.4", roomy); status.ShouldCompact {
		t.Fatalf("display items must not trigger compaction on their own: %#v", status)
	}
}
