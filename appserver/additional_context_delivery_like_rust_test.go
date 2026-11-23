package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/turn"
)

// Rust #62602: omission or null keeps the current set; a supplied map, even
// empty, replaces it.
func TestMergeAdditionalContextNilKeepsAndEmptyClearsLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	threadID := "thread-context-updates"
	if _, _, _ = router.mergeAdditionalContext(threadID, map[string]turn.AdditionalContextEntry{
		"automation_info": {Value: "run one", Kind: turn.AdditionalContextApplication},
	}, false); false {
		t.Fatal("unreachable")
	}
	router.additionalContextMu.Lock()
	stored := len(router.additionalContext[threadID])
	router.additionalContextMu.Unlock()
	if stored != 1 {
		t.Fatalf("stored context after update = %d, want 1", stored)
	}

	if _, _, _ = router.mergeAdditionalContext(threadID, nil, false); false {
		t.Fatal("unreachable")
	}
	router.additionalContextMu.Lock()
	stored = len(router.additionalContext[threadID])
	_, kept := router.additionalContext[threadID]["automation_info"]
	router.additionalContextMu.Unlock()
	if stored != 1 || !kept {
		t.Fatalf("stored context after omission = %d (kept=%t), want the current set", stored, kept)
	}

	router.mergeAdditionalContext(threadID, map[string]turn.AdditionalContextEntry{}, false)
	router.additionalContextMu.Lock()
	stored = len(router.additionalContext[threadID])
	router.additionalContextMu.Unlock()
	if stored != 0 {
		t.Fatalf("stored context after empty map = %d, want the set cleared", stored)
	}

	router.mergeAdditionalContext(threadID, nil, false)
	router.additionalContextMu.Lock()
	stored = len(router.additionalContext[threadID])
	router.additionalContextMu.Unlock()
	if stored != 0 {
		t.Fatalf("stored context after a later omission = %d, want it to stay cleared", stored)
	}
}

// Rust #62602: a later turn without a context update keeps the current set,
// while an empty map clears it. Recorded history stays either way, and the
// kept set is what compaction can restore.
func TestRuntimeRouterAdditionalContextKeptUntilClearedLikeRust(t *testing.T) {
	router, agent, sink, _ := newAdditionalContextTestRouter(t, "")
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello context",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	runTurn := func(id int64, context map[string]turn.AdditionalContextEntry, prompt string) *model.AgentRequest {
		t.Helper()
		start := router.Handle(requestWithParams(t, IntID(id), MethodTurnStart, turn.TurnStartParams{
			ThreadID:          threadID,
			Prompt:            prompt,
			AdditionalContext: context,
		}))
		if start.Error != nil {
			t.Fatalf("turn start error: %+v", start.Error)
		}
		request := waitForRuntimeAgentRequest(t, agent)
		waitForTurnCompletedStatus(t, sink, start.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
		return &request
	}
	stored := func() map[string]storedAdditionalContext {
		router.additionalContextMu.Lock()
		defer router.additionalContextMu.Unlock()
		out := map[string]storedAdditionalContext{}
		for key, value := range router.additionalContext[threadID] {
			out[key] = value
		}
		return out
	}

	first := runTurn(2, map[string]turn.AdditionalContextEntry{
		"automation_info": {Value: "run one", Kind: turn.AdditionalContextApplication},
		"browser_info":    {Value: "tab one", Kind: turn.AdditionalContextUntrusted},
	}, "first")
	if !strings.Contains(first.Instructions, "run one") {
		t.Fatalf("first instructions = %q, want the application context", first.Instructions)
	}
	if outputs := additionalContextToolOutputs(t, first.InputItems); len(outputs) != 1 || outputs[0]["output"] != "tab one" {
		t.Fatalf("first tool outputs = %#v, want the untrusted context", outputs)
	}

	kept := runTurn(3, nil, "second")
	if len(stored()) != 2 {
		t.Fatalf("stored context after omission = %#v, want the current set", stored())
	}
	keptText := additionalContextAllItemText(t, kept.InputItems)
	if strings.Count(keptText, "run one") != 1 || strings.Count(keptText, "tab one") != 1 {
		t.Fatalf("second input items = %q, want recorded context once", keptText)
	}

	cleared := runTurn(4, map[string]turn.AdditionalContextEntry{}, "third")
	if len(stored()) != 0 {
		t.Fatalf("stored context after empty map = %#v, want the set cleared", stored())
	}
	clearedText := additionalContextAllItemText(t, cleared.InputItems)
	if strings.Count(clearedText, "run one") != 1 || strings.Count(clearedText, "tab one") != 1 {
		t.Fatalf("third input items = %q, want recorded history to stay", clearedText)
	}

	runTurn(5, nil, "fourth")
	if len(stored()) != 0 {
		t.Fatalf("stored context after a later omission = %#v, want it to stay cleared", stored())
	}
}

// additionalContextAllItemText joins every request input item so a test can
// find recorded context wherever it was replayed.
func additionalContextAllItemText(t *testing.T, items []any) string {
	t.Helper()
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal input items: %v", err)
	}
	return string(encoded)
}

// Rust #62602 applies the same Option semantics to turn/steer.
func TestTurnSteerAdditionalContextNilKeepsAndEmptyClearsLikeRust(t *testing.T) {
	router, _, _, _ := newAdditionalContextTestRouter(t, "")
	threadID := "thread-steer-updates"
	router.mergeAdditionalContext(threadID, map[string]turn.AdditionalContextEntry{
		"automation_info": {Value: "run one", Kind: turn.AdditionalContextApplication},
	}, false)
	stored := func() int {
		router.additionalContextMu.Lock()
		defer router.additionalContextMu.Unlock()
		return len(router.additionalContext[threadID])
	}

	router.inputItemsFromTurnSteer(&turn.TurnSteerParams{ThreadID: threadID, Prompt: "one"}, true)
	if got := stored(); got != 1 {
		t.Fatalf("stored context after a steer without an update = %d, want the current set", got)
	}
	router.inputItemsFromTurnSteer(&turn.TurnSteerParams{
		ThreadID:          threadID,
		Prompt:            "two",
		AdditionalContext: map[string]turn.AdditionalContextEntry{},
	}, true)
	if got := stored(); got != 0 {
		t.Fatalf("stored context after an empty steer update = %d, want the set cleared", got)
	}
}
