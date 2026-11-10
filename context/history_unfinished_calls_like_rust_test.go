package context

import (
	"testing"

	"codex_go/eventmap"
)

// TestUnfinishedRestoredCallsGetAbortedOutputsLikeRust pins Rust #60519: a
// forked or recovered history can restore a call before its output was
// recorded, and the model input must carry a synthetic aborted output instead
// of a dangling call.
func TestUnfinishedRestoredCallsGetAbortedOutputsLikeRust(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(
		HistoryItem{Kind: eventmap.ResponseMessage, Role: "user", ID: "u1", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: "fix it"}}},
		HistoryItem{Kind: eventmap.ResponseOther, ID: "patch-1", WebSearchAction: "custom_tool_call"},
		HistoryItem{Kind: eventmap.ResponseOther, ID: "shell-1", WebSearchAction: "local_shell_call"},
		HistoryItem{Kind: eventmap.ResponseOther, ID: "lookup-1", WebSearchAction: "tool_search_call"},
	)
	prompt := manager.ForPrompt(false)
	want := []struct {
		action string
		id     string
		abort  string
	}{
		{"custom_tool_call_output", "patch-1", "aborted"},
		{"function_call_output", "shell-1", "aborted"},
		{"tool_search_output", "lookup-1", ""},
	}
	if len(prompt) != 1+2*len(want) {
		t.Fatalf("prompt len = %d, want the user message plus a call/output pair per call: %#v", len(prompt), prompt)
	}
	for i, expected := range want {
		callIndex := 1 + i*2
		outputIndex := callIndex + 1
		output := prompt[outputIndex]
		if output.Kind != eventmap.ResponseOther || output.WebSearchAction != expected.action || output.ID != expected.id {
			t.Fatalf("output %d = %#v, want %s for %s", outputIndex, output, expected.action, expected.id)
		}
		if output.ImageResult != expected.abort {
			t.Fatalf("output %d aborted marker = %q, want %q", outputIndex, output.ImageResult, expected.abort)
		}
	}
}

// TestRecordedCallsKeepTheirOwnOutputs pins the other half of Rust #60519: a
// call whose output was already recorded keeps that output and gains no
// synthetic duplicate.
func TestRecordedCallsKeepTheirOwnOutputs(t *testing.T) {
	manager := NewHistoryManager()
	manager.RecordItems(
		HistoryItem{Kind: eventmap.ResponseMessage, Role: "user", ID: "u1", Content: []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: "patch it"}}},
		HistoryItem{Kind: eventmap.ResponseOther, ID: "patch-1", WebSearchAction: "custom_tool_call"},
		HistoryItem{Kind: eventmap.ResponseOther, ID: "patch-1", WebSearchAction: "custom_tool_call_output", ImageResult: "done"},
	)
	prompt := manager.ForPrompt(false)
	if len(prompt) != 3 {
		t.Fatalf("prompt len = %d, want the user message and the recorded pair: %#v", len(prompt), prompt)
	}
	if prompt[2].WebSearchAction != "custom_tool_call_output" || prompt[2].ImageResult != "done" {
		t.Fatalf("recorded output was replaced: %#v", prompt[2])
	}
}
