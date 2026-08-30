package appserver

import "testing"

// TestClockToolsAreBuiltinControlToolsLikeRust pins #41331: clock.curr_time /
// clock.sleep calls are built-in control tools, so they are not classified as
// dynamic tools nor as MCP tools.
func TestClockToolsAreBuiltinControlToolsLikeRust(t *testing.T) {
	for _, tool := range []string{"curr_time", "sleep"} {
		item := &ThreadItem{
			Type: "function_call",
			Name: tool,
			Data: map[string]any{"namespace": "clock"},
		}
		if threadItemLooksLikeDynamic(item) {
			t.Fatalf("clock.%s should not be classified as a dynamic tool", tool)
		}
		if threadItemLooksLikeMCP(item) {
			t.Fatalf("clock.%s should not be classified as an MCP tool", tool)
		}
		if got := threadItemWireType(item); got == "dynamicToolCall" || got == "mcpToolCall" {
			t.Fatalf("clock.%s wire type = %q, want a built-in tool type", tool, got)
		}
	}
}
