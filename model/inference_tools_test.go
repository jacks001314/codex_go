package model

import "testing"

// TestInferenceToolsChangesFollowFullSpecsAcrossTurnsLikeRust mirrors the Rust
// test `inference_tools_changes_follow_full_specs_across_turns`
// (codex-rs/core/src/client_tests.rs, Rust #50964). The client retains the last
// full model-visible tool list across turns and connection resets, the first
// list only establishes a baseline, and schema changes, additions, reordering,
// parameter changes and emptying the list all count as changes. Go compares the
// serialized model-visible tool list (what the sampling request sends) with the
// same equality semantics as Rust's `Arc<[ToolSpec]>`.
func TestInferenceToolsChangesFollowFullSpecsAcrossTurnsLikeRust(t *testing.T) {
	alpha := map[string]any{"type": "function", "name": "alpha", "description": "Original"}
	beta := map[string]any{"type": "function", "name": "beta", "description": "Original"}
	outputChanged := map[string]any{
		"type": "function", "name": "alpha", "description": "Original",
		"output_schema": map[string]any{"type": "object"},
	}
	paramsChanged := map[string]any{
		"type": "function", "name": "beta", "description": "Original",
		"parameters": map[string]any{"type": "string", "description": "Changed parameters"},
	}

	runner := NewResponsesAgentRunner(nil)
	cases := []struct {
		tools    []any
		expected bool
	}{
		{[]any{alpha}, false},
		{[]any{outputChanged}, true},
		{[]any{outputChanged, beta}, true},
		{[]any{outputChanged, beta}, false},
		{[]any{beta, alpha}, true},          // ordering change
		{[]any{paramsChanged, alpha}, true}, // parameter change
		{[]any{}, true},                     // emptied: an absent list equals an empty one
		{[]any{}, false},
	}
	for index, tc := range cases {
		if index == 4 {
			// Rust switches the fallback transport and drops the session here;
			// the state lives on the client, so the retained baseline survives.
			runner = runner.WithStreamHandler(nil)
		}
		if got := runner.InferenceToolsChanged("thread-1", tc.tools); got != tc.expected {
			t.Fatalf("step %d: InferenceToolsChanged = %v, want %v", index, got, tc.expected)
		}
	}
	// A clone (Rust's `client.new_session()`) still compares against the retained
	// baseline rather than starting over.
	if runner.InferenceToolsChanged("thread-1", []any{}) {
		t.Fatalf("unchanged list on a new session reported a change")
	}
	// Each conversation keeps its own baseline.
	if runner.InferenceToolsChanged("thread-2", []any{alpha}) {
		t.Fatalf("first list of a new conversation reported a change")
	}
}
