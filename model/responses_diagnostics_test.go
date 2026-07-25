package model

import "testing"

func TestResponsesInputSummaryReportsTypesAndToolPairingWithoutContent(t *testing.T) {
	summary := responsesInputSummary([]any{
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "secret prompt"}}},
		map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call-1", "arguments": "secret command"},
		map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "secret output"},
		map[string]any{"type": "tool_search_call", "call_id": "call-2", "arguments": map[string]any{"query": "secret query"}},
	})
	types, ok := summary["types"].([]string)
	if !ok || len(types) != 4 || types[0] != "message" || types[3] != "tool_search_call" {
		t.Fatalf("summary types = %#v", summary["types"])
	}
	if summary["unmatched_calls"] != 1 || summary["unmatched_outputs"] != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	for _, forbidden := range []string{"secret prompt", "secret command", "secret output", "secret query"} {
		for _, itemType := range types {
			if itemType == forbidden {
				t.Fatalf("summary leaked %q", forbidden)
			}
		}
	}
}
