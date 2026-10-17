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

func TestValidateHookMcpEnvironmentLikeRust(t *testing.T) {
	config := mcp.ServerConfig{EnvironmentID: "executor-a"}
	if err := validateHookMcpEnvironment("docs", config, ""); err != nil {
		t.Fatalf("host-local hook error = %v", err)
	}
	if err := validateHookMcpEnvironment("docs", config, "executor-a"); err != nil {
		t.Fatalf("matching environment error = %v", err)
	}
	err := validateHookMcpEnvironment("docs", config, "executor-b")
	if err == nil || err.Error() != "MCP server `docs` is running in environment `executor-a`, expected `executor-b`" {
		t.Fatalf("mismatched environment error = %v", err)
	}
}
