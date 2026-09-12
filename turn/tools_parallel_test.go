package turn

import (
	"testing"

	"codex_go/mcp"
	"codex_go/tool"
)

// TestMCPToolParallelOptInFollowsServerConfigLikeRust mirrors Rust's
// McpServerMetadata::supports_parallel_tool_calls -> ToolInfo
// -> per-call parallel gate: a server that opts in advertises its tools as
// safe for parallel execution, while an unset server keeps the read-only rule.
func TestMCPToolParallelOptInFollowsServerConfigLikeRust(t *testing.T) {
	service := mcp.NewMCPService(&mcp.RuntimeConfig{
		Servers: map[string]mcp.ServerRegistration{
			"parallel-srv": {
				Name: "parallel-srv",
				Config: mcp.ServerConfig{
					Enabled:                   true,
					Command:                   "mcp-parallel",
					SupportsParallelToolCalls: true,
				},
			},
			"serial-srv": {
				Name:   "serial-srv",
				Config: mcp.ServerConfig{Enabled: true, Command: "mcp-serial"},
			},
		},
	})
	modelVisible := true
	options := DefaultToolRegistryOptions(t.TempDir())
	options.MCPService = service
	options.MCPExposure = tool.ExposureModelVisible
	options.MCPTools = []mcp.RuntimeToolInfo{
		{
			ServerName:        "parallel-srv",
			CallableNamespace: "parallel-srv",
			CallableName:      "write_thing",
			Tool:              mcp.RuntimeTool{Name: "write_thing", ModelVisible: &modelVisible},
		},
		{
			ServerName:        "serial-srv",
			CallableNamespace: "serial-srv",
			CallableName:      "write_thing",
			Tool:              mcp.RuntimeTool{Name: "write_thing", ModelVisible: &modelVisible},
		},
	}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	router := tool.NewRouter(registry)
	// The model-visible namespace keeps the legacy `mcp__` prefix.
	parallelTool := tool.NamespacedName("mcp__parallel_srv", "write_thing")
	serialTool := tool.NamespacedName("mcp__serial_srv", "write_thing")
	if _, ok := registry.Lookup(parallelTool); !ok {
		t.Fatalf("registered names: %#v", registry.Names())
	}
	if !router.SupportsParallel(parallelTool) {
		t.Fatal("a server with supports_parallel_tool_calls did not mark its tool parallel-safe")
	}
	if router.SupportsParallel(serialTool) {
		t.Fatal("a server without the opt-in marked a non-read-only tool parallel-safe")
	}
}
