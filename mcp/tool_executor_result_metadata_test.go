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

// Mirrors Rust #45409's retained cell window: a nested Code Mode call reports the
// window its cell started in, while every other call reports the turn's window.
func TestOriginWindowIDPrefersTheRetainedWindowLikeRust(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{WindowID: "thread-1:1"})
	turnCall := &tool.Invocation{CallID: "call-turn"}
	if got := executor.originWindowID(turnCall); got != "thread-1:1" {
		t.Fatalf("turn window = %q", got)
	}
	retained := &tool.Invocation{
		CallID:  "call-nested",
		Source:  "code_mode",
		Context: map[string]any{tool.OriginWindowIDContextKey: "thread-1:0"},
	}
	if got := executor.originWindowID(retained); got != "thread-1:0" {
		t.Fatalf("retained window = %q, want thread-1:0", got)
	}
	blank := &tool.Invocation{CallID: "call-blank", Context: map[string]any{tool.OriginWindowIDContextKey: "  "}}
	if got := executor.originWindowID(blank); got != "thread-1:1" {
		t.Fatalf("blank override must fall back to the turn window: %q", got)
	}
	if got := NewToolExecutor(&ToolExecutorOptions{}).originWindowID(nil); got != "" {
		t.Fatalf("executor without a window = %q", got)
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
