package telemetry

import (
	"testing"

	"codex_go/tool"
	"codex_go/turn"
)

// TestMCPCallOutcomeForExecutionUsesRequestErrorCodeLikeRust mirrors Rust
// #54094: a request failure classified before the error became a string keeps
// its bounded code in the outcome used for metrics and spans.
func TestMCPCallOutcomeForExecutionUsesRequestErrorCodeLikeRust(t *testing.T) {
	execution := &turn.ToolExecutionResult{
		Invocation:    &tool.Invocation{CallID: "call-1", ToolName: tool.PlainName("mcp_tool")},
		TelemetryTags: map[string]string{"mcp_server": "server"},
	}
	execution.TelemetryTags[tool.RequestErrorCodeTelemetryTag] = "transport_closed"
	outcome, ok := MCPCallOutcomeForExecution(execution)
	if !ok {
		t.Fatal("execution was not classified as an MCP call")
	}
	want := MCPCallOutcome{Status: "error", ErrorType: MCPCallErrorTypeMCPRequest, ErrorCode: "transport_closed"}
	if outcome != want {
		t.Fatalf("outcome = %#v, want %#v", outcome, want)
	}
	attributes := MCPCallSpanAttributes(outcome, nil)
	if attributes[MCPCallErrorTypeSpanAttr] != MCPCallErrorTypeMCPRequest || attributes[MCPCallErrorCodeSpanAttr] != "transport_closed" {
		t.Fatalf("span attributes = %#v", attributes)
	}
	// A coded tool-result outcome is never overwritten.
	execution.Output = &tool.Output{Data: map[string]any{"mcpToolCall": true, "isError": true}}
	outcome, _ = MCPCallOutcomeForExecution(execution)
	if outcome.ErrorCode != MCPCallErrorCodeUnknown {
		t.Fatalf("tool-result fallback = %#v, want the unknown code", outcome)
	}
}
