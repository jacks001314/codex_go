package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/plugin"
)

// TestRuntimeRouterRetiredPluginEMAAuthOverlayDisablesContributionLikeRust
// mirrors Rust #49260 (`unsupported_plugin_ema_auth`): the per-plugin
// enterprise registration overlay
// `[plugins."<id>".mcp_servers.<server>.ema_auth]` was removed from the host
// schema. A trusted config that still declares it must not grant the plugin
// server enterprise authority; declaring the retired overlay disables that
// server instead, so it never enters the runtime MCP catalog while its siblings
// still load. The overlay is accepted (and ignored) rather than rejected so a
// stale managed policy cannot brick configuration loading.
func TestRuntimeRouterRetiredPluginEMAAuthOverlayDisablesContributionLikeRust(t *testing.T) {
	home := t.TempDir()
	pluginRoot := filepath.Join(home, "plugins", "api-plugin")
	if err := os.MkdirAll(filepath.Join(pluginRoot, ".codex-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), []byte(`{"name":"api-plugin"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"mcpServers":{"retired":{"type":"http","url":"https://retired.example/mcp"},"kept":{"type":"http","url":"https://kept.example/mcp"}}}`
	if err := os.WriteFile(filepath.Join(pluginRoot, ".mcp.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	configToml := `[features]
apps = false
plugins = true

[plugins."api-plugin@test-market".mcp_servers.retired.ema_auth]
url = "https://retired.example/mcp"
client_id = "retired-client"
authorization_server_issuer = "https://as.example"
resource = "https://retired.example"
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(configToml), 0o600); err != nil {
		t.Fatal(err)
	}
	plugins := plugin.NewPluginService()
	plugins.SetCodexHome(home)
	plugins.AddPlugin(plugin.PluginDetail{Summary: plugin.PluginSummary{
		ID:              "api-plugin@test-market",
		Name:            "api-plugin",
		MarketplaceName: "test-market",
		Source:          plugin.PluginSource{Type: "local", Path: pluginRoot},
		Installed:       true,
		Enabled:         true,
	}})
	router := NewRuntimeRouter(RuntimeServices{
		Config:  config.NewConfigService(home),
		Plugins: plugins,
		Skills:  NewSkillsService(nil),
		MCP:     mcp.NewMCPService(nil),
	})
	t.Cleanup(func() { _ = router.Close() })
	router.configureMCPFromConfig()

	statuses := router.mcpServiceForThread("thread-retired-ema", nil).ConfiguredStatuses()
	names := make([]string, 0, len(statuses))
	for _, status := range statuses {
		names = append(names, status.Name)
	}
	if len(names) != 1 || names[0] != "kept" {
		t.Fatalf("runtime MCP servers = %v, want only the kept server; declaring the retired plugin EMA overlay must disable its server", names)
	}
}
