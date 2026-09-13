package turn

import (
	"testing"

	"codex_go/mcp"
	"codex_go/plugin"
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

// TestBuiltinToolParallelFlagsLikeRust pins the per-tool parallel-safety flags
// against Rust's `ToolHandler::supports_parallel_tool_calls` overrides (the
// trait default is false): exec_command/write_stdin/test_sync/view_image/
// tool_search/MCP-resource tools opt in, everything else - including dynamic
// tools, the skills extension, plugin-install tools, plan, request_user_input
// and the multi-agent tools - stays serial.
func TestBuiltinToolParallelFlagsLikeRust(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.PluginInstallCandidates = []plugin.DiscoverableInfo{{ID: "parity", Name: "Parity"}}
	// A configured MCP server registers the MCP resource tools and makes the
	// orchestrator skills tools available.
	options.MCPService = mcp.NewMCPService(&mcp.RuntimeConfig{
		Servers: map[string]mcp.ServerRegistration{
			"srv": {Name: "srv", Config: mcp.ServerConfig{Enabled: true, Command: "mcp-srv"}},
		},
	})
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	router := tool.NewRouter(registry)
	cases := []struct {
		name tool.ToolName
		want bool
	}{
		{tool.PlainName("exec_command"), true},
		{tool.PlainName("write_stdin"), true},
		{tool.PlainName("apply_patch"), false},
		{tool.PlainName("update_plan"), false},
		{tool.PlainName("request_user_input"), false},
		{tool.PlainName("get_context_remaining"), false},
		{tool.PlainName("tool_search"), true},
		{tool.PlainName("list_mcp_resources"), true},
		{tool.PlainName("list_mcp_resource_templates"), true},
		{tool.PlainName("read_mcp_resource"), true},
		{tool.PlainName("list_available_plugins_to_install"), false},
		{tool.NamespacedName("skills", "list"), false},
		{tool.NamespacedName("skills", "read"), false},
		{tool.NamespacedName("multi_agent_v1", "spawn_agent"), false},
		{tool.NamespacedName("multi_agent_v1", "wait_agent"), false},
	}
	for _, tc := range cases {
		if _, ok := registry.Spec(tc.name); !ok {
			t.Errorf("tool %q is not registered", tc.name.Key())
			continue
		}
		if got := router.SupportsParallel(tc.name); got != tc.want {
			t.Errorf("SupportsParallel(%q) = %v, want %v", tc.name.Key(), got, tc.want)
		}
	}
}
