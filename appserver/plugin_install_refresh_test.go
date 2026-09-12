package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/plugin"
)

// TestRuntimeRouterPluginInstallRefreshesMCPRuntimesLikeRust covers Rust #42593:
// installing a plugin reloads the effective plugin state so threads loaded
// before the install pick up the plugin's MCP servers.
func TestRuntimeRouterPluginInstallRefreshesMCPRuntimesLikeRust(t *testing.T) {
	home := t.TempDir()
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{
		Summary: plugin.PluginSummary{
			Name:            "sample",
			MarketplaceName: "test",
			MCPServers:      []string{"mcp-a"},
		},
		MCPServers: []string{"mcp-a"},
	})
	router := NewRuntimeRouter(RuntimeServices{
		Plugins: plugins,
		Config:  config.NewConfigService(home),
	})
	if router.mcpConfigManaged.Load() {
		t.Fatal("precondition: managed MCP config should be inactive before install")
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodPluginInstall, plugin.PluginInstallParams{PluginID: "sample@test"}))
	if response.Error != nil {
		t.Fatalf("plugin install error = %+v", response.Error)
	}
	// effectivePluginsChanged -> configureMCPFromConfig marks the managed MCP
	// config active and invalidates the per-thread runtimes.
	if !router.mcpConfigManaged.Load() {
		t.Fatal("plugin install did not refresh the MCP runtime")
	}
}
