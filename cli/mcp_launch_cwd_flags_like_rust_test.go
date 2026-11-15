package cli

import "testing"

func TestParseAppServerMCPLaunchCwdFlagsLikeRust(t *testing.T) {
	parsed, err := Parse([]string{
		"app-server",
		"--plugin-mcp-launch-cwd", "sample@market", "docs", "/host/plugin-docs",
		"--plugin-mcp-launch-cwd=sample@market", "search", "/host/search",
		"--mcp-launch-cwd", "configured", "/usr/bin/node", "/host/configured",
		"--mcp-launch-cwd=other", "/usr/bin/python", "/host/other",
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	pluginEntries := parsed.AppServer.PluginMCPLaunchCwds
	if len(pluginEntries) != 2 {
		t.Fatalf("plugin launch entries = %#v", pluginEntries)
	}
	if pluginEntries[0] != (PluginMCPLaunchCwd{PluginID: "sample@market", Server: "docs", Directory: "/host/plugin-docs"}) {
		t.Fatalf("first plugin entry = %#v", pluginEntries[0])
	}
	if pluginEntries[1] != (PluginMCPLaunchCwd{PluginID: "sample@market", Server: "search", Directory: "/host/search"}) {
		t.Fatalf("second plugin entry = %#v", pluginEntries[1])
	}
	configured := parsed.AppServer.MCPLaunchCwds
	if len(configured) != 2 {
		t.Fatalf("configured launch entries = %#v", configured)
	}
	if configured[0] != (ConfiguredMCPLaunchCwd{Server: "configured", Executable: "/usr/bin/node", Directory: "/host/configured"}) {
		t.Fatalf("first configured entry = %#v", configured[0])
	}
	if configured[1] != (ConfiguredMCPLaunchCwd{Server: "other", Executable: "/usr/bin/python", Directory: "/host/other"}) {
		t.Fatalf("second configured entry = %#v", configured[1])
	}
}

func TestParseAppServerMCPLaunchCwdRequiresThreeValuesLikeRust(t *testing.T) {
	if _, err := Parse([]string{"app-server", "--plugin-mcp-launch-cwd", "sample@market", "docs"}); err == nil {
		t.Fatal("an incomplete plugin launch triple was accepted")
	}
	if _, err := Parse([]string{"app-server", "--mcp-launch-cwd", "docs", "/usr/bin/node"}); err == nil {
		t.Fatal("an incomplete configured launch triple was accepted")
	}
}
