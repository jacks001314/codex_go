package mcp

import (
	"testing"

	"codex_go/tool"
)

// Mirrors Rust ToolInvocation::originating_call's item id (#40866): a direct
// call reports the Responses item it was dispatched from, and a nested Code Mode
// call inherits its cell's origin because the delegate clones the parent
// invocation's context.
func TestInvocationOriginItemIDLikeRust(t *testing.T) {
	parent := &tool.Invocation{
		CallID:  "call-exec",
		Context: map[string]any{tool.OriginItemIDContextKey: "ctc_before_compaction"},
	}
	if got := invocationOriginItemID(parent); got != "ctc_before_compaction" {
		t.Fatalf("direct origin item = %q", got)
	}

	nestedContext := map[string]any{}
	for key, value := range parent.Context {
		nestedContext[key] = value
	}
	nested := &tool.Invocation{CallID: "call-nested", Source: "code_mode", Context: nestedContext}
	if got := invocationOriginItemID(nested); got != "ctc_before_compaction" {
		t.Fatalf("nested origin item = %q", got)
	}

	if got := invocationOriginItemID(&tool.Invocation{CallID: "call-plain"}); got != "" {
		t.Fatalf("invocation without an origin = %q", got)
	}
	if got := invocationOriginItemID(nil); got != "" {
		t.Fatalf("nil invocation = %q", got)
	}
}

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
