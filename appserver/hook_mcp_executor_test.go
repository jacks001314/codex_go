package appserver

import (
	"context"
	"strings"
	"testing"

	"codex_go/mcp"
)

func TestMcpHookResponseTextJoinsTextAndStructuredContent(t *testing.T) {
	response := &mcp.MCPToolCallResponse{Content: []mcp.MCPToolCallContent{
		{Type: "text", Text: "first"},
		{Type: "text", Text: "  "},
	}}
	if got := mcpHookResponseText(response); got != "first" {
		t.Fatalf("mcpHookResponseText = %q, want first", got)
	}
	if got := mcpHookResponseText(nil); got != "" {
		t.Fatalf("nil response text = %q, want empty", got)
	}
}

func TestRuntimeRouterExecuteRejectsMissingHookCall(t *testing.T) {
	router := NewDefaultRuntimeRouterWithOptions(nil, "", nil)
	if _, err := router.Execute(context.Background(), HookMcpCall{}); err == nil {
		t.Fatal("empty hook call should be rejected")
	}
	if _, err := router.Execute(context.Background(), HookMcpCall{Server: "apps", Tool: ""}); err == nil {
		t.Fatal("missing hook tool should be rejected")
	}
}

func TestRuntimeRouterExecuteFailsFastWhenMCPServiceUnavailable(t *testing.T) {
	router := NewDefaultRuntimeRouterWithOptions(nil, "", nil)
	_, err := router.Execute(context.Background(), HookMcpCall{Server: "apps", Tool: "calendar"})
	if err == nil {
		t.Fatal("unavailable MCP runtime should fail immediately")
	}
	if !strings.Contains(err.Error(), "not cataloged or connected") && !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unexpected error = %v", err)
	}
}
