package mcp

import (
	"testing"
)

// Mirrors Rust #46010's `McpToolOutput::tool_result_metadata`: the raw MCP
// result `_meta` is exposed for the internal executed-tool-call record only when
// the caller allowed the capture.
func TestToolExecutorResultMetadataCaptureLikeRust(t *testing.T) {
	meta := map[string]any{"provider/custom": map[string]any{"items": []any{1}}}
	response := &MCPToolCallResponse{Content: []MCPToolCallContent{{Type: "text", Text: "ok"}}, Meta: meta}

	allowed := NewToolExecutor(&ToolExecutorOptions{ServerName: "codex_apps", CaptureResultMetadata: true})
	if got := allowed.capturedResultMetadata(response); got == nil {
		t.Fatal("an allowed capture must expose the raw result metadata")
	}
	if !allowed.captureResultMetadata {
		t.Fatal("the executor options did not enable the capture")
	}

	disallowed := NewToolExecutor(&ToolExecutorOptions{ServerName: "codex_apps"})
	if got := disallowed.capturedResultMetadata(response); got != nil {
		t.Fatalf("a disabled capture exposed %#v", got)
	}
	if got := allowed.capturedResultMetadata(nil); got != nil {
		t.Fatalf("a nil response exposed %#v", got)
	}
	if got := allowed.capturedResultMetadata(&MCPToolCallResponse{}); got != nil {
		t.Fatalf("a result without metadata exposed %#v", got)
	}
}
