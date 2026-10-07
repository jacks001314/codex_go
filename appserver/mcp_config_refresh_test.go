package appserver

// Rust parity: codex-rs/app-server/src/mcp_refresh.rs and
// codex-rs/app-server/src/request_processors/config_processor.rs (#49260).

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/mcp"
)

// mcpConfigRefreshTestEnterpriseConfig is the fixture for the #49260 fail-closed
// reload coverage: one ordinary server plus one enterprise (EMA) server admitted
// by a trusted (user) layer.
const mcpConfigRefreshTestEnterpriseConfig = `[features]
use_xaa = true

[mcp_enterprise_managed_auth.idp]
issuer = "https://idp.example"
client_id = "idp-client"

[mcp_servers.docs]
command = "mcp-docs"

[mcp_servers.enterprise]
url = "https://resource.example/mcp"
auth = "ema_auth"
oauth_resource = "https://resource.example/mcp"

[mcp_servers.enterprise.oauth]
client_id = "mcp-client"
`

func writeMCPConfigRefreshTestConfig(t *testing.T, home string, body string) {
	t.Helper()
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
}

func newMCPConfigRefreshTestRouter(home string) *RuntimeRouter {
	return NewRuntimeRouter(RuntimeServices{
		Config: config.NewConfigService(home),
		MCP:    mcp.NewMCPService(nil),
	})
}

// threadlessMCPServerNames lists the process-wide MCP catalog, which is what a
// reload publishes and what a threadless status request reports.
func threadlessMCPServerNames(t *testing.T, router *RuntimeRouter, id int64) map[string]bool {
	t.Helper()
	status := router.Handle(requestWithParams(t, IntID(id), MethodMCPServerStatusList, mcp.MCPListServerStatusParams{}))
	if status.Error != nil {
		t.Fatalf("status = %+v", status)
	}
	response, ok := status.Result.(*mcp.MCPListServerStatusResponse)
	if !ok {
		t.Fatalf("status result = %#v", status.Result)
	}
	names := map[string]bool{}
	for _, server := range response.Data {
		names[server.Name] = true
	}
	return names
}

// TestRuntimeRouterMCPConfigReloadFailsClosedOnUnloadableConfigLikeRust covers
// Rust #49260: a strict reload that cannot load the configuration reports the
// failure and leaves the loaded sessions without enterprise MCP authority while
// their ordinary MCP servers survive.
func TestRuntimeRouterMCPConfigReloadFailsClosedOnUnloadableConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	writeMCPConfigRefreshTestConfig(t, home, mcpConfigRefreshTestEnterpriseConfig)
	router := newMCPConfigRefreshTestRouter(home)

	if reload := router.Handle(requestWithParams(t, IntID(1), MethodConfigMCPServerReload, map[string]any{})); reload.Error != nil {
		t.Fatalf("initial reload = %+v", reload)
	}
	if names := threadlessMCPServerNames(t, router, 2); !names["docs"] || !names["enterprise"] {
		t.Fatalf("initial MCP servers = %v, want docs and enterprise", names)
	}

	writeMCPConfigRefreshTestConfig(t, home, "[features")

	reload := router.Handle(requestWithParams(t, IntID(3), MethodConfigMCPServerReload, map[string]any{}))
	if reload.Error == nil {
		t.Fatal("reload of an unloadable configuration reported no error")
	}
	names := threadlessMCPServerNames(t, router, 4)
	if names["enterprise"] {
		t.Fatalf("enterprise MCP server survived a failed reload: %v", names)
	}
	if !names["docs"] {
		t.Fatalf("ordinary MCP server was dropped by the fail-closed reload: %v", names)
	}
}

// TestRuntimeRouterReloadUserConfigFailsClosedOnUnloadableConfigLikeRust covers
// Rust reload_user_config_inner (#49260): when the user configuration cannot be
// rebuilt, the loaded sessions lose enterprise MCP authority. The test drives
// the production function that handleConfigBatchWrite invokes for a write with
// reloadUserConfig set.
func TestRuntimeRouterReloadUserConfigFailsClosedOnUnloadableConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	writeMCPConfigRefreshTestConfig(t, home, mcpConfigRefreshTestEnterpriseConfig)
	router := newMCPConfigRefreshTestRouter(home)

	if reload := router.Handle(requestWithParams(t, IntID(1), MethodConfigMCPServerReload, map[string]any{})); reload.Error != nil {
		t.Fatalf("initial reload = %+v", reload)
	}

	writeMCPConfigRefreshTestConfig(t, home, "[features")
	router.reloadUserConfigForLoadedThreads()

	names := threadlessMCPServerNames(t, router, 2)
	if names["enterprise"] {
		t.Fatalf("enterprise MCP server survived a failed user-config reload: %v", names)
	}
	if !names["docs"] {
		t.Fatalf("ordinary MCP server was dropped by the fail-closed reload: %v", names)
	}
}

// TestRuntimeRouterMCPConfigReloadDisablesEnterpriseServerRevokedByRequirementsLikeRust
// covers the "policy revokes access" half of #49260: a reload whose managed
// requirements no longer admit the enterprise server publishes it disabled while
// the admitted server survives.
func TestRuntimeRouterMCPConfigReloadDisablesEnterpriseServerRevokedByRequirementsLikeRust(t *testing.T) {
	home := t.TempDir()
	writeMCPConfigRefreshTestConfig(t, home, mcpConfigRefreshTestEnterpriseConfig)
	requirements := "[mcp_servers.docs.identity]\ncommand = \"mcp-docs\"\n"
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatalf("write requirements error = %v", err)
	}
	router := newMCPConfigRefreshTestRouter(home)

	if reload := router.Handle(requestWithParams(t, IntID(1), MethodConfigMCPServerReload, map[string]any{})); reload.Error != nil {
		t.Fatalf("reload = %+v", reload)
	}
	names := threadlessMCPServerNames(t, router, 2)
	if names["enterprise"] {
		t.Fatalf("enterprise MCP server was not revoked by managed requirements: %v", names)
	}
	if !names["docs"] {
		t.Fatalf("admitted MCP server was dropped by managed requirements: %v", names)
	}
}
