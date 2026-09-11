package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors Rust #44336: tool-result metadata is bounded, redacted, and serialized
// as the raw snapshot or the omission marker.
func TestToolResultMetadataBoundsAndRedacts(t *testing.T) {
	var zero ToolResultMetadata
	if !zero.IsNone() || zero.IsSome() {
		t.Fatal("zero metadata must be absent")
	}

	snapshot := map[string]any{"provider/custom": map[string]any{"items": []any{1, nil}}}
	metadata := NewToolResultMetadata(snapshot)
	if !metadata.IsSome() {
		t.Fatal("bounded snapshot must be present")
	}
	if metadata.String() != "ToolResultMetadata([redacted])" {
		t.Fatalf("String() = %q, want redacted", metadata.String())
	}
	call := NewExecutedToolCall("tool", map[string]any{})
	if !call.SetToolResultMetadata(metadata) {
		t.Fatal("SetToolResultMetadata must report present metadata")
	}
	encoded, err := json.Marshal(call)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"tool_result_metadata":{"provider/custom":{"items":[1,null]}}`) {
		t.Fatalf("Marshal() = %s", encoded)
	}

	oversized := NewToolResultMetadata(strings.Repeat("x", MaxExecutedToolCallMetadataBytes+1))
	call.SetToolResultMetadata(oversized)
	encoded, err = json.Marshal(call)
	if err != nil {
		t.Fatalf("Marshal(oversized) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"tool_result_metadata":"omitted_due_to_size_limit"`) {
		t.Fatalf("Marshal(oversized) = %s", encoded)
	}
}

// Metadata is shed before source evidence and calls when the prompt budget is
// exceeded, and omission markers themselves are optional.
func TestBoundExecutedToolCallsShedsMetadataBeforeSourcesAndCalls(t *testing.T) {
	item := &AgentItem{Type: "function_call", Name: "tool", CallID: "call", Arguments: `{}`}
	RecordExecutedToolCall(item)
	calls := item.ExecutedToolCalls()
	calls[0].SetToolResultSources(NewToolResultSources([]ToolResultSource{{Type: "document", ID: "R0"}}))
	// Keep the individual snapshot under the limit, but let the call total cross it.
	calls[0].SetToolResultMetadata(NewToolResultMetadata(strings.Repeat("m", MaxExecutedToolCallMetadataBytes-64)))
	if !calls[0].HasToolResultMetadata() {
		t.Fatal("raw snapshot must be retained before bounding")
	}
	item.ReplaceExecutedToolCalls(calls)

	bounded := BoundExecutedToolCallsForPrompt([]any{item})
	if len(bounded) != 1 {
		t.Fatalf("bounded items = %d, want 1", len(bounded))
	}
	boundedItem := bounded[0].(*AgentItem)
	if executedToolCallMetadataBytes(boundedItem) > MaxExecutedToolCallMetadataBytes {
		t.Fatal("bounded metadata exceeds the prompt budget")
	}
	boundedCalls := boundedItem.ExecutedToolCalls()
	if len(boundedCalls) != 1 {
		t.Fatalf("bounded calls = %d, want the original call retained", len(boundedCalls))
	}
	if boundedCalls[0].truncation != nil {
		t.Fatal("shedding metadata must not truncate the call itself")
	}
	if !boundedCalls[0].HasToolResultSources() {
		t.Fatal("source evidence must survive metadata shedding")
	}
	encoded, err := json.Marshal(boundedCalls[0])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"tool_result_metadata":"omitted_due_to_size_limit"`) {
		t.Fatalf("raw metadata was not replaced by the omission marker: %s", encoded)
	}
}

func TestToolResultMetadataDestinationFiltering(t *testing.T) {
	for _, allowed := range []string{
		"https://api.openai.com/v1",
		"https://chatgpt.com/backend-api/codex",
		"https://chat.openai.com",
		"https://chatgpt-staging.com",
		"https://staging.chatgpt.com",
	} {
		if !toolResultMetadataDestinationAllowed(allowed) {
			t.Fatalf("destination %q must allow raw metadata", allowed)
		}
	}
	for _, denied := range []string{
		"https://evilchatgpt.com",
		"https://api.openai.com.evil.example",
		"http://api.openai.com/v1",
		"https://example.com/v1",
		"",
	} {
		if toolResultMetadataDestinationAllowed(denied) {
			t.Fatalf("destination %q must not allow raw metadata", denied)
		}
	}

	serialized := map[string]any{
		internalChatMessageMetadataPassthroughField: map[string]any{
			executedToolCallsField: []any{
				map[string]any{"name": "tool", "tool_result_metadata": map[string]any{"k": "v"}},
			},
		},
	}
	clearToolResultMetadataInPromptItem(serialized)
	calls := serialized[internalChatMessageMetadataPassthroughField].(map[string]any)[executedToolCallsField].([]any)
	if _, present := calls[0].(map[string]any)[toolResultMetadataField]; present {
		t.Fatal("destination filter must strip serialized raw metadata")
	}
}
