package compact

import (
	"reflect"
	"strings"
	"testing"
)

func toolSearchHistory() []Item {
	tools := []any{map[string]any{
		"type":        "namespace",
		"name":        "codex_app",
		"description": "Codex app tools.",
		"tools": []any{map[string]any{
			"type":          "function",
			"name":          "oversized_dynamic_tool",
			"description":   strings.Repeat("x", 20_000),
			"parameters":    map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
			"strict":        false,
			"defer_loading": true,
		}},
	}}
	return []Item{
		{Type: "message", Role: "user", Text: "find the tool"},
		{Type: "tool_search_call", CallID: "tool-search-1", Text: `{"query":"oversized deferred tool"}`},
		{Type: "tool_search_output", CallID: "tool-search-1", Data: map[string]any{
			"status":    "completed",
			"execution": "client",
			"tools":     tools,
		}},
	}
}

// Rust parity: codex-rs/core/tests/suite/compact_remote_trimming.rs at #44255.
func TestEstimateItemTokensChargesToolSearchArgumentsJSONLikeRust(t *testing.T) {
	objectArgs := Item{Type: "tool_search_call", CallID: "c", Data: map[string]any{
		"arguments": map[string]any{"query": "deferred tool"},
	}}
	encoded := `{"query":"deferred tool"}`
	if got, want := EstimateItemTokens(&objectArgs), compactTokensFromBytes(len(encoded)); got != want {
		t.Fatalf("object arguments estimate = %d, want %d", got, want)
	}
	textArgs := Item{Type: "tool_search_call", CallID: "c", Text: encoded}
	if got, want := EstimateItemTokens(&textArgs), compactTokensFromBytes(len(encoded)); got != want {
		t.Fatalf("text arguments estimate = %d, want %d", got, want)
	}
}

// Rust parity: codex-rs/core/tests/suite/compact_remote_trimming.rs at #44255.
func TestTrimFunctionCallHistoryToFitContextWindowEmptiesOversizedToolSearchOutputLikeRust(t *testing.T) {
	history := toolSearchHistory()
	expectedTools := history[2].Data["tools"]

	wide, rewritten := TrimFunctionCallHistoryToFitContextWindow(history, 200_000, "", false)
	if rewritten != 0 {
		t.Fatalf("wide window rewrote %d outputs, want 0", rewritten)
	}
	if !reflect.DeepEqual(wide[2].Data["tools"], expectedTools) {
		t.Fatalf("wide window tools = %#v, want the discovered catalog", wide[2].Data["tools"])
	}

	narrow, rewritten := TrimFunctionCallHistoryToFitContextWindow(history, 2_000, "", false)
	if rewritten != 1 {
		t.Fatalf("narrow window rewrote %d outputs, want 1", rewritten)
	}
	output := narrow[2]
	if output.CallID != "tool-search-1" || output.Data["status"] != "completed" || output.Data["execution"] != "client" {
		t.Fatalf("trimmed tool search output lost identity: %#v", output)
	}
	if tools, ok := output.Data["tools"].([]any); !ok || len(tools) != 0 {
		t.Fatalf("trimmed tools = %#v, want an empty array", output.Data["tools"])
	}
	if narrow[1].CallID != "tool-search-1" || narrow[1].Type != "tool_search_call" {
		t.Fatalf("tool search call changed: %#v", narrow[1])
	}
	if EstimateItemTokens(&history[2]) <= EstimateItemTokens(&output) {
		t.Fatalf("tool search output estimate = %d, want larger than trimmed %d", EstimateItemTokens(&history[2]), EstimateItemTokens(&output))
	}
}

// Rust parity: codex-rs/core/src/compact_remote_history.rs at #44255. A retained
// client tool output only stops the rewrite pass when the feature is enabled; a
// plain function output at the same position is rewritten either way.
func TestTrimFunctionCallHistoryToFitContextWindowStopsAtRetainedToolOutputLikeRust(t *testing.T) {
	retained := clientAuthoredToolOutput("retained", "shell", strings.Repeat("x", 8_000))
	history := []Item{
		{Type: "function_call", CallID: "older", Name: "exec_command", Text: "{}"},
		{Type: "function_call_output", CallID: "older", Text: "older output"},
		retained,
	}

	trimmed, rewritten := TrimFunctionCallHistoryToFitContextWindow(history, 100, "", false)
	if rewritten != 1 || trimmed[2].Text != ContextWindowTruncatedOutputMessage {
		t.Fatalf("feature off: rewritten = %d output = %q, want 1 rewritten", rewritten, trimmed[2].Text)
	}

	trimmed, rewritten = TrimFunctionCallHistoryToFitContextWindow(history, 100, "", true)
	if rewritten != 0 {
		t.Fatalf("feature on: rewritten = %d, want 0", rewritten)
	}
	if trimmed[2].Text != retained.Text {
		t.Fatalf("feature on: retained output = %q, want it preserved", trimmed[2].Text)
	}
	if trimmed[0].Text != "{}" || trimmed[1].Text != "older output" {
		t.Fatalf("feature on: older history changed: %#v", trimmed[:2])
	}
}

// Rust parity: codex-rs/core/tests/suite/compact_remote_trimming.rs at #44255.
func TestTrimFunctionCallHistoryToFitContextWindowUsesBaseInstructionsLikeRust(t *testing.T) {
	short := "session base instructions"
	medium := short + " " + strings.Repeat("x", 16_000)
	long := short + " " + strings.Repeat("x", 40_000)
	trailing := strings.Repeat("x", 12_000)
	history := []Item{
		{Type: "message", Role: "user", Text: "older user"},
		{Type: "function_call", CallID: "retained", Name: "exec_command", Text: "{}"},
		{Type: "function_call_output", CallID: "retained", Text: "retained output"},
		{Type: "message", Role: "user", Text: "user boundary"},
		{Type: "function_call", CallID: "trailing", Name: "exec_command", Text: "{}"},
		{Type: "function_call_output", CallID: "trailing", Text: trailing},
	}
	for _, testCase := range []struct {
		name         string
		instructions string
		wantRewrites int
	}{
		{name: "short", instructions: short},
		{name: "medium", instructions: medium},
		{name: "long", instructions: long, wantRewrites: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			trimmed, rewritten := TrimFunctionCallHistoryToFitContextWindow(history, 12_000, testCase.instructions, false)
			if rewritten != testCase.wantRewrites {
				t.Fatalf("rewritten = %d, want %d", rewritten, testCase.wantRewrites)
			}
			if len(trimmed) != len(history) {
				t.Fatalf("history length = %d, want %d", len(trimmed), len(history))
			}
			if trimmed[2].Text != "retained output" {
				t.Fatalf("retained output = %q, want it preserved", trimmed[2].Text)
			}
			wantTrailing := trailing
			if testCase.wantRewrites == 1 {
				wantTrailing = ContextWindowTruncatedOutputMessage
			}
			if trimmed[5].Text != wantTrailing {
				t.Fatalf("trailing output = %q, want %q", trimmed[5].Text, wantTrailing)
			}
		})
	}
}
