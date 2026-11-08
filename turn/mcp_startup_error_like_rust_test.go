package turn

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/mcp"
	"codex_go/model"
	"codex_go/tool"
)

// TestCachedToolCallSurfacesMCPServerStartupErrorLikeRust covers Rust #60038: a
// cached MCP tool stays advertised after its server failed to start, and calling
// it reports the startup failure to the model instead of the generic
// unavailable-tool text. The failure is this call's result, so the turn keeps
// going instead of aborting.
func TestCachedToolCallSurfacesMCPServerStartupErrorLikeRust(t *testing.T) {
	service := mcp.NewMCPService(&mcp.RuntimeConfig{
		Servers: map[string]mcp.ServerRegistration{
			"broken": {
				Name: "broken",
				Config: mcp.ServerConfig{
					Enabled:        true,
					Command:        filepath.Join(t.TempDir(), "definitely-missing-mcp-server"),
					StartupTimeout: 10 * time.Second,
				},
			},
		},
	})
	defer func() { _ = service.Close() }()

	// A regular status read is what leaves the server in its failed state while
	// its cached tool definitions stay advertised.
	if _, err := service.ListStatusChecked(&mcp.MCPListServerStatusParams{}); err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}

	executor := mcp.NewToolExecutor(&mcp.ToolExecutorOptions{
		Service:    service,
		ServerName: "broken",
		ToolInfo:   &mcp.MCPToolInfo{Name: "do"},
	})
	registry := tool.NewRegistry()
	if err := registry.Register(executor); err != nil {
		t.Fatalf("register MCP executor: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})

	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		ID:        "call-1",
		Type:      "function_call",
		Namespace: "broken",
		Name:      "do",
		CallID:    "call-1",
		Arguments: `{}`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v, want the startup failure to become a tool result", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %#v, want one result", results)
	}
	output := results[0].Output
	if output == nil || output.Success {
		t.Fatalf("output = %#v, want a failed tool result", output)
	}
	if !strings.Contains(output.Body, "Could not start MCP server `broken`") {
		t.Fatalf("tool output = %q, want the MCP startup failure", output.Body)
	}
	// The underlying cause travels with the message, mirroring Rust's
	// format!("{error:#}") chain instead of a flattened one-line reason.
	if !strings.Contains(output.Body, "definitely-missing-mcp-server") {
		t.Fatalf("tool output = %q, want the startup cause preserved", output.Body)
	}
	if response := results[0].Response; response == nil || response.Output.Text() == "" {
		t.Fatalf("response = %#v, want the failure recorded as the call output", response)
	}
}
