package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/plugin"
)

func TestMCPSharedReplCwdsRequiresBothFeaturesLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]any
		want   bool
	}{
		{name: "both", values: map[string]any{"features": map[string]any{"mcp_shared_repl_cwd": true, "mcp_connection_sharing": true}}, want: true},
		// Rust #63821 made the launch-directory feature stable and on by default.
		{name: "both default", values: map[string]any{}, want: true},
		// Rust #62552 made connection sharing stable and on by default, so an
		// absent sharing key now satisfies the second half of the gate.
		{name: "default sharing", values: map[string]any{"features": map[string]any{"mcp_shared_repl_cwd": true}}, want: true},
		{name: "sharing disabled", values: map[string]any{"features": map[string]any{"mcp_shared_repl_cwd": true, "mcp_connection_sharing": false}}, want: false},
		{name: "default repl cwd", values: map[string]any{"features": map[string]any{"mcp_connection_sharing": true}}, want: true},
		{name: "explicit false", values: map[string]any{"features": map[string]any{"mcp_shared_repl_cwd": false, "mcp_connection_sharing": true}}, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := mcpSharedReplCwdsEnabled(testCase.values); got != testCase.want {
				t.Fatalf("mcpSharedReplCwdsEnabled() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestConfiguredMCPLaunchCwdRequiresMatchingExecutableLikeRust(t *testing.T) {
	launch := &config.MCPLaunchCwds{Configured: map[string]config.MCPLaunchCwd{
		"docs": {Executable: "node", Directory: "/host/docs"},
	}}
	runtime := &mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		"docs":     {Name: "docs", Config: mcp.ServerConfig{Command: "node", Enabled: true}},
		"explicit": {Name: "explicit", Config: mcp.ServerConfig{Command: "node", CWD: "/declared", Enabled: true}},
		"other":    {Name: "other", Config: mcp.ServerConfig{Command: "python", Enabled: true}},
	}}
	applyConfiguredMCPLaunchCwds(runtime, launch)
	if got := runtime.Servers["docs"].Config.CWD; got != "/host/docs" {
		t.Fatalf("docs cwd = %q, want the launch directory", got)
	}
	if got := runtime.Servers["explicit"].Config.CWD; got != "/declared" {
		t.Fatalf("explicit cwd was replaced: %q", got)
	}
	if got := runtime.Servers["other"].Config.CWD; got != "" {
		t.Fatalf("nonmatching executable received a cwd: %q", got)
	}
}

func TestPluginMCPLaunchCwdSkipsAgentPluginsLikeRust(t *testing.T) {
	launch := &config.MCPLaunchCwds{Plugins: map[string]map[string]string{
		"sample@market": {"docs": "/host/plugin-docs"},
	}}
	server := &mcp.ServerConfig{Command: "node"}
	applyPluginMCPLaunchCwd(server, plugin.MCPServerContribution{PluginID: "sample@market", Name: "docs", AgentPlugin: false}, launch)
	if server.CWD != "/host/plugin-docs" {
		t.Fatalf("plugin cwd = %q", server.CWD)
	}
	agentServer := &mcp.ServerConfig{Command: "node", CWD: "/agent/declared"}
	applyPluginMCPLaunchCwd(agentServer, plugin.MCPServerContribution{PluginID: "sample@market", Name: "docs", AgentPlugin: true}, launch)
	if agentServer.CWD != "/agent/declared" {
		t.Fatalf("Agent Plugin cwd was replaced: %q", agentServer.CWD)
	}
}

func TestRuntimeRouterCarriesProcessLocalLaunchCwdsLikeRust(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "node")
	launchDir := t.TempDir()
	router := NewDefaultRuntimeRouterWithOptions(nil, t.TempDir(), &RuntimeRouterOptions{
		MCPLaunchCwds: &config.MCPLaunchCwds{Configured: map[string]config.MCPLaunchCwd{
			"docs": {Executable: executable, Directory: launchDir},
		}},
	})
	values := map[string]any{
		"features": map[string]any{"mcp_shared_repl_cwd": true, "mcp_connection_sharing": true},
		"mcp_servers": map[string]any{
			"docs": map[string]any{"command": executable},
		},
	}
	runtime := router.runtimeMCPConfigForThread(mcp.NewHostMCPServerContributionContext("", "", ""), values, t.TempDir(), nil, nil)
	if runtime == nil {
		t.Fatal("runtime config is nil")
	}
	if got := runtime.Servers["docs"].Config.CWD; got != launchDir {
		t.Fatalf("docs cwd = %q, want %q", got, launchDir)
	}
	values["features"] = map[string]any{"mcp_shared_repl_cwd": false, "mcp_connection_sharing": true}
	disabled := router.runtimeMCPConfigForThread(mcp.NewHostMCPServerContributionContext("", "", ""), values, t.TempDir(), nil, nil)
	if got := disabled.Servers["docs"].Config.CWD; got != "" {
		t.Fatalf("launch cwd applied without the feature: %q", got)
	}
}
