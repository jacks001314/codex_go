package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/mcp"
)

// TestBundledReplCleanupLeavesDormantServerLikeRust mirrors Rust #61437: the
// bundled cleanup returns an empty success without starting a never-used REPL,
// while an ordinary hook still waits for startup.
func TestBundledReplCleanupLeavesDormantServerLikeRust(t *testing.T) {
	service := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		"node_repl": {Config: mcp.ServerConfig{
			Command:        "mcp-definitely-missing-node-repl",
			Enabled:        true,
			StartupTimeout: time.Second,
		}},
	}})
	defer service.Close()
	router := NewRuntimeRouter(RuntimeServices{MCP: service})

	output, err := router.Execute(context.Background(), HookMcpCall{
		Server:                 "node_repl",
		Tool:                   "turn_ended",
		SkipDormantReplCleanup: true,
	})
	if err != nil || output.Text != "" {
		t.Fatalf("dormant cleanup = %#v, %v; want empty success", output, err)
	}
	if !service.IsDormantPrivateStdio("node_repl", "") {
		t.Fatal("bundled cleanup started the dormant REPL server")
	}

	if _, err := router.Execute(context.Background(), HookMcpCall{Server: "node_repl", Tool: "turn_ended"}); err == nil {
		t.Fatal("ordinary hook should still wait for server startup")
	}
}

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
