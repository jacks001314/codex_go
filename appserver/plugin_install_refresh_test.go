package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/mcp"
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

// TestRuntimeRouterPluginInstallReloadsStagedUserConfigLikeRust also covers
// Rust #42593's "pending user-config changes" half: a config.toml edit made
// while the server was running reaches the process-wide MCP runtime after a
// plugin install.
func TestRuntimeRouterPluginInstallReloadsStagedUserConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	writeMCPConfigRefreshTestConfig(t, home, "[mcp_servers.staged]\ncommand = \"staged-mcp\"\n")
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
		MCP:     mcp.NewMCPService(nil),
	})
	if names := threadlessMCPServerNames(t, router, 1); names["staged"] {
		t.Fatalf("staged configuration was applied before install: %v", names)
	}

	response := router.Handle(requestWithParams(t, IntID(2), MethodPluginInstall, plugin.PluginInstallParams{PluginID: "sample@test"}))
	if response.Error != nil {
		t.Fatalf("plugin install error = %+v", response.Error)
	}
	if names := threadlessMCPServerNames(t, router, 3); !names["staged"] {
		t.Fatalf("plugin install did not reload the staged user config: %v", names)
	}
}

// TestRuntimeRouterPluginInstallSurvivesConfigReloadFailureLikeRust covers Rust
// #42593's failure policy: a configuration reload that cannot complete is
// logged, but the installation itself still succeeds.
func TestRuntimeRouterPluginInstallSurvivesConfigReloadFailureLikeRust(t *testing.T) {
	home := t.TempDir()
	writeMCPConfigRefreshTestConfig(t, home, "[features")
	plugins := plugin.NewPluginService()
	plugins.AddPlugin(plugin.PluginDetail{
		Summary: plugin.PluginSummary{
			Name:            "sample",
			MarketplaceName: "test",
		},
	})
	router := NewRuntimeRouter(RuntimeServices{
		Plugins: plugins,
		Config:  config.NewConfigService(home),
		MCP:     mcp.NewMCPService(nil),
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodPluginInstall, plugin.PluginInstallParams{PluginID: "sample@test"}))
	if response.Error != nil {
		t.Fatalf("plugin install failed with an unloadable config: %+v", response.Error)
	}
	if router.mcpConfigManaged.Load() {
		t.Fatal("unloadable config unexpectedly published a managed MCP runtime")
	}
}
