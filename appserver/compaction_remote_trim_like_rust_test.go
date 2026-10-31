package appserver

import (
	"strings"
	"testing"

	"codex_go/compact"
)

// Rust parity: codex-rs/core/tests/suite/compact_remote_trimming.rs at #44255.
// The background (streamed) compaction snapshot rewrites oversized tool-search
// catalogs to an empty tools array instead of dropping conversation items.
func TestBackgroundCompactionInputTrimsToolSearchOutputLikeRust(t *testing.T) {
	tools := []any{map[string]any{
		"type": "namespace",
		"name": "codex_app",
		"tools": []any{map[string]any{
			"type":        "function",
			"name":        "oversized_dynamic_tool",
			"description": strings.Repeat("x", 20_000),
		}},
	}}
	history := []compact.Item{
		{Type: "message", Role: "user", Text: "find the tool"},
		{Type: "tool_search_call", CallID: "tool-search-1", Text: `{"query":"oversized deferred tool"}`},
		{Type: "tool_search_output", CallID: "tool-search-1", Data: map[string]any{
			"status":    "completed",
			"execution": "client",
			"tools":     tools,
		}},
	}

	wide := captureBackgroundCompactionInput(history, len(history), "", compactionResponseWindow{}, "", "gpt-5", "hash", 0, 200_000, false)
	if got := wide.History[2].Data["tools"]; len(got.([]any)) == 0 {
		t.Fatalf("wide window tools = %#v, want the discovered catalog", got)
	}

	narrow := captureBackgroundCompactionInput(history, len(history), "", compactionResponseWindow{}, "", "gpt-5", "hash", 0, 2_000, false)
	if len(narrow.History) != len(history) {
		t.Fatalf("narrow history length = %d, want %d", len(narrow.History), len(history))
	}
	trimmed := narrow.History[2]
	if trimmed.CallID != "tool-search-1" || trimmed.Data["status"] != "completed" || trimmed.Data["execution"] != "client" {
		t.Fatalf("trimmed tool search output lost identity: %#v", trimmed)
	}
	if tools, ok := trimmed.Data["tools"].([]any); !ok || len(tools) != 0 {
		t.Fatalf("trimmed tools = %#v, want an empty array", trimmed.Data["tools"])
	}
	payload := inputItemsFromCompactItems(narrow.History)
	output, ok := payload[2].(map[string]any)
	if !ok {
		t.Fatalf("serialized tool search output = %#v, want an object", payload[2])
	}
	if tools, ok := output["tools"].([]any); !ok || len(tools) != 0 {
		t.Fatalf("serialized tools = %#v, want an empty array", output["tools"])
	}
	if output["call_id"] != "tool-search-1" || output["status"] != "completed" || output["execution"] != "client" {
		t.Fatalf("serialized tool search output = %#v, want identity preserved", output)
	}
}

// Rust parity: codex-rs/core/tests/suite/compact_remote_trimming.rs at #44255.
// The estimate charges the session base instructions, so longer instructions move
// the rewrite boundary to the trailing output.
func TestBackgroundCompactionInputTrimUsesBaseInstructionsLikeRust(t *testing.T) {
	short := "session base instructions"
	long := short + " " + strings.Repeat("x", 40_000)
	trailing := strings.Repeat("x", 12_000)
	history := []compact.Item{
		{Type: "message", Role: "user", Text: "older user"},
		{Type: "function_call", CallID: "retained", Name: "exec_command", Text: "{}"},
		{Type: "function_call_output", CallID: "retained", Text: "retained output"},
		{Type: "message", Role: "user", Text: "user boundary"},
		{Type: "function_call", CallID: "trailing", Name: "exec_command", Text: "{}"},
		{Type: "function_call_output", CallID: "trailing", Text: trailing},
	}

	shortInput := captureBackgroundCompactionInput(history, len(history), "", compactionResponseWindow{}, short, "gpt-5", "hash", 0, 12_000, false)
	if shortInput.History[5].Text != trailing {
		t.Fatalf("short instructions trailing output = %q, want it preserved", shortInput.History[5].Text)
	}
	if shortInput.History[2].Text != "retained output" {
		t.Fatalf("short instructions retained output = %q, want it preserved", shortInput.History[2].Text)
	}

	longInput := captureBackgroundCompactionInput(history, len(history), "", compactionResponseWindow{}, long, "gpt-5", "hash", 0, 12_000, false)
	if longInput.History[5].Text != compact.ContextWindowTruncatedOutputMessage {
		t.Fatalf("long instructions trailing output = %q, want the truncation placeholder", longInput.History[5].Text)
	}
	if longInput.History[2].Text != "retained output" {
		t.Fatalf("long instructions retained output = %q, want it preserved", longInput.History[2].Text)
	}
	if longInput.BaseInstructions != long {
		t.Fatalf("captured base instructions = %q, want the session instructions", longInput.BaseInstructions)
	}
}
