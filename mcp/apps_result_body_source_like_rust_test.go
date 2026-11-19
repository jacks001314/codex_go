package mcp

import "testing"

// Mirrors Rust capture_mcp_result's source (#62392): the snapshot keeps the raw
// content blocks and the error flag the projection validates.
func TestResultBodySourceFromMCPResponseLikeRust(t *testing.T) {
	response := &MCPToolCallResponse{
		Content: []MCPToolCallContent{
			{Raw: map[string]any{"type": "text", "text": "hello"}},
			{Raw: map[string]any{"type": "text", "text": "world", "_meta": map[string]any{"codex/encryptedContent": true}}},
		},
		StructuredContent: map[string]any{"kind": "result"},
	}
	source := resultBodySourceFromMCPResponse(response)
	if source == nil {
		t.Fatal("resultBodySourceFromMCPResponse() = nil")
	}
	if len(source.Content) != 2 {
		t.Fatalf("content blocks = %d, want 2", len(source.Content))
	}
	first, ok := source.Content[0].(map[string]any)
	if !ok || first["text"] != "hello" {
		t.Fatalf("first block = %#v", source.Content[0])
	}
	if source.IsError {
		t.Fatal("IsError = true for a successful result")
	}
	if resultBodySourceFromMCPResponse(nil) != nil {
		t.Fatal("resultBodySourceFromMCPResponse(nil) != nil")
	}
}
