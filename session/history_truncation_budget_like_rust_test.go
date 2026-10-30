package session

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/utils"
)

// Rust #44248 saves the originating history truncation budget on function and
// custom tool outputs and reuses it when replaying history under another model.
func TestApplyHistoryTruncationBudgetLikeRust(t *testing.T) {
	body := strings.Repeat("diagnostic line\n", 300)
	item := Item{
		ID:       "fco_budget",
		Type:     "tool_output",
		Name:     "exec_command",
		Text:     body,
		Data:     map[string]any{"output": body},
		Metadata: map[string]any{"payloadKind": "function"},
	}
	policy := utils.TokensPolicy(20)
	ApplyHistoryTruncationBudget(&item, &policy, true)

	if got := historyTestTokenLimit(t, item); got != 24 {
		t.Fatalf("saved history budget = %d, want 24 (20 + serialization allowance)", got)
	}
	bounded := stringValue(item.Data, "output")
	if bounded == body {
		t.Fatal("oversized function output was not truncated")
	}
	if !strings.Contains(bounded, "tokens truncated") {
		t.Fatalf("bounded output is missing the truncation marker: %q", bounded)
	}

	replayed := InputItemsFromItems([]Item{item}, &HistoryBuildOptions{IncludeToolOutputs: true})
	if len(replayed) != 1 {
		t.Fatalf("replayed items = %#v", replayed)
	}
	output, ok := replayed[0].(map[string]any)
	replayedBody, _ := output["output"].(string)
	if !ok || output["type"] != "function_call_output" || strings.Count(replayedBody, "tokens truncated") != 1 {
		t.Fatalf("replayed output = %#v, want saved bounded body", replayed[0])
	}
	if _, leaked := output["harness_metadata"]; leaked {
		t.Fatalf("harness metadata leaked into model history: %#v", output)
	}
}

func TestApplyHistoryTruncationBudgetPreservesToolOverrideLikeRust(t *testing.T) {
	body := strings.Repeat("mcp output\n", 200)
	item := Item{
		ID:   "fco_override",
		Type: "function_call_output",
		Name: "mcp_tool",
		Data: map[string]any{
			"output": body,
			"harness_metadata": json.RawMessage(
				`{"fallback_token_limit_override":3,"future_metadata":"kept"}`,
			),
		},
	}
	policy := utils.TokensPolicy(100)
	ApplyHistoryTruncationBudget(&item, &policy, true)

	if got := historyTestTokenLimit(t, item); got != 3 {
		t.Fatalf("tool override = %d, want preserved 3", got)
	}
	metadata := harnessMetadata(item.Data)
	if metadata["future_metadata"] != "kept" {
		t.Fatalf("unrelated harness metadata was dropped: %#v", metadata)
	}
	if bounded := stringValue(item.Data, "output"); bounded == body || !strings.Contains(bounded, "tokens truncated") {
		t.Fatalf("override was not applied to the body: %q", bounded)
	}
}

func TestApplyHistoryTruncationBudgetConvertsBytesBeforeTokensLikeRust(t *testing.T) {
	item := Item{Type: "tool_output", Text: "small"}
	policy := utils.BytesPolicy(101)
	ApplyHistoryTruncationBudget(&item, &policy, true)

	if got := historyTestTokenLimit(t, item); got != 31 {
		t.Fatalf("byte-policy history budget = %d, want 31", got)
	}
}

func TestHistoryReplayUsesSavedBudgetForRawToolOutputLikeRust(t *testing.T) {
	body := strings.Repeat("rolled out output\n", 300)
	raw, err := json.Marshal(map[string]any{
		"type":    "function_call_output",
		"call_id": "call-budget",
		"output":  body,
	})
	if err != nil {
		t.Fatalf("marshal raw output: %v", err)
	}
	items := []Item{
		{ID: "call-budget", Type: "function_call", CallID: "call-budget", Name: "exec_command", Text: "{}"},
		{
			ID:     "fco_raw_budget",
			Type:   "function_call_output",
			CallID: "call-budget",
			Raw:    raw,
			Data: map[string]any{
				"harness_metadata": json.RawMessage(`{"fallback_token_limit_override":8}`),
			},
		},
	}
	replayed := InputItemsFromItems(items, &HistoryBuildOptions{IncludeToolOutputs: true})
	var output any
	for _, value := range replayed {
		item, _ := value.(map[string]any)
		if item["type"] == "function_call_output" {
			output = item["output"]
			break
		}
	}
	text, _ := output.(string)
	if text == "" || text == body {
		t.Fatalf("replayed raw tool output = %#v, want saved-budget truncation", output)
	}
	if !strings.Contains(text, "tokens truncated") {
		t.Fatalf("raw replay is missing the saved-budget truncation marker: %q", text)
	}
}

func historyTestTokenLimit(t *testing.T, item Item) int {
	t.Helper()
	metadata := harnessMetadata(item.Data)
	value, ok := metadata[historyTruncationTokenLimitKey]
	if !ok {
		t.Fatalf("missing %s: %#v", historyTruncationTokenLimitKey, metadata)
	}
	limit, ok := historyTruncationTokenLimit(&item)
	if !ok {
		t.Fatalf("invalid history budget %#v", value)
	}
	return limit
}
