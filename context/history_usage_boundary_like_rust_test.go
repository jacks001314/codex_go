package context

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/eventmap"
)

func usageBoundaryUser(text string) HistoryItem {
	return HistoryItem{Kind: eventmap.ResponseMessage, Role: "user", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: text}}}
}

func usageBoundaryAssistant(text string) HistoryItem {
	return HistoryItem{Kind: eventmap.ResponseMessage, Role: "assistant", Content: []eventmap.ContentItem{{Kind: eventmap.ContentOutputText, Text: text}}}
}

func usageBoundaryReasoning(size int) HistoryItem {
	raw, err := json.Marshal(map[string]any{"type": "reasoning", "summary": []any{}, "encrypted_content": strings.Repeat("A", size)})
	if err != nil {
		panic(err)
	}
	return HistoryItem{Kind: eventmap.ResponseReasoning, RawContent: []string{string(raw)}}
}

func usageBoundaryToolOutput(id, text string) HistoryItem {
	return HistoryItem{Kind: eventmap.ResponseOther, ID: id, WebSearchAction: "function_call_output", ImageResult: text}
}

func sumUsageBoundaryTokens(items []HistoryItem) int64 {
	var total int64
	for i := range items {
		total += EstimateItemTokens(&items[i])
	}
	return total
}

// Mirrors Rust #58404 items_since_last_usage_include_initial_input: before the
// first usable usage report every retained item must be estimated.
func TestItemsSinceLastUsageIncludeInitialInputLikeRust(t *testing.T) {
	manager := NewHistoryManager()
	initial := usageBoundaryUser("no model output yet")
	manager.RecordItems(initial)
	if got, want := manager.EstimatedTokensSinceLastUsage(), EstimateItemTokens(&initial); got != want {
		t.Fatalf("estimated tokens since usage = %d, want %d", got, want)
	}
	if boundary := manager.UsageBoundary(); boundary != 0 {
		t.Fatalf("initial usage boundary = %d, want 0", boundary)
	}
}

// Mirrors Rust items_since_last_usage_include_user_and_tool_output.
func TestItemsSinceLastUsageIncludeUserAndToolOutputLikeRust(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(usageBoundaryAssistant("already counted by API"))
	manager.UpdateTokenInfoWithBoundary(TokenUsage{}, nil, 0)
	user := usageBoundaryUser("new user message")
	output := usageBoundaryToolOutput("call-tail", "new tool output")
	manager.RecordItems(user, output)
	got := manager.EstimatedTokensSinceLastUsage()
	want := EstimateItemTokens(&user) + EstimateItemTokens(&output)
	if got != want {
		t.Fatalf("estimated tokens since usage = %d, want %d", got, want)
	}
	if boundary := manager.UsageBoundary(); boundary != 1 {
		t.Fatalf("usage boundary = %d, want 1", boundary)
	}
}

// Mirrors Rust #58404: a successful response covers the request input even when
// it returns no output items.
func TestUpdateTokenInfoWithBoundaryCoversEmptyResponseInputLikeRust(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(usageBoundaryUser("first"), usageBoundaryAssistant("answer"), usageBoundaryUser("next"))
	inputItemCount := len(manager.RawItems())
	manager.UpdateTokenInfoWithBoundary(TokenUsage{TotalTokens: 100}, nil, inputItemCount)
	if boundary := manager.UsageBoundary(); boundary != inputItemCount {
		t.Fatalf("usage boundary = %d, want %d", boundary, inputItemCount)
	}
	if got := manager.EstimatedTokensSinceLastUsage(); got != 0 {
		t.Fatalf("estimated tokens since usage = %d, want 0", got)
	}
}

// Mirrors Rust #58404: spending-only usage updates preserve the coverage
// boundary, so retained output from a failed response stays estimated.
func TestSpendingOnlyUpdatePreservesUsageBoundaryLikeRust(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(usageBoundaryUser("first"), usageBoundaryAssistant("answer"))
	inputItemCount := len(manager.RawItems())
	manager.UpdateTokenInfoWithBoundary(TokenUsage{TotalTokens: 100}, nil, inputItemCount)
	partial := usageBoundaryReasoning(2000)
	manager.RecordItems(partial)
	manager.UpdateTokenInfo(TokenUsage{TotalTokens: 15}, nil)
	user := usageBoundaryUser("next")
	manager.RecordItems(user)
	got := manager.EstimatedTokensSinceLastUsage()
	want := EstimateItemTokens(&partial) + EstimateItemTokens(&user)
	if got != want {
		t.Fatalf("estimated tokens since usage = %d, want %d", got, want)
	}
}

// Mirrors Rust #58404: local output recorded after the last model item is not
// covered by the usage report for that response.
func TestUsageBoundaryExcludesItemsAfterFinalModelOutputLikeRust(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(usageBoundaryUser("first"), usageBoundaryAssistant("answer"))
	inputItemCount := len(manager.RawItems())
	output := usageBoundaryToolOutput("tool-tail", "new tool output")
	manager.RecordItems(usageBoundaryAssistant("next response"), output)
	manager.UpdateTokenInfoWithBoundary(TokenUsage{TotalTokens: 100}, nil, inputItemCount)
	got := manager.EstimatedTokensSinceLastUsage()
	want := EstimateItemTokens(&output)
	if got != want {
		t.Fatalf("estimated tokens since usage = %d, want %d", got, want)
	}
}

// Mirrors Rust reset_last_usage_boundary: rewritten history falls back to the
// index after the latest model output.
func TestResetUsageBoundaryUsesLastModelOutputLikeRust(t *testing.T) {
	manager := NewHistoryManager()
	manager.Replace([]HistoryItem{usageBoundaryUser("first"), usageBoundaryAssistant("answer"), usageBoundaryUser("tail")})
	if boundary := manager.UsageBoundary(); boundary != 2 {
		t.Fatalf("rewritten usage boundary = %d, want 2", boundary)
	}
	if got := manager.EstimatedTokensSinceLastUsage(); got != sumUsageBoundaryTokens(manager.ItemsSinceLastUsage()) {
		t.Fatalf("estimated tokens since usage = %d, want the tail sum", got)
	}
	manager.Replace([]HistoryItem{usageBoundaryUser("only input")})
	if boundary := manager.UsageBoundary(); boundary != 1 {
		t.Fatalf("input-only rewritten boundary = %d, want 1", boundary)
	}
}
