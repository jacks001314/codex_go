package mcp

import (
	"testing"

	"codex_go/apps"
)

func pluginApprovalPtr(mode apps.AppToolApproval) *apps.AppToolApproval { return &mode }

func pluginManifestSamplePolicy() ServerConfig {
	return ServerConfig{
		URL:                      "https://sample.example/mcp",
		Enabled:                  true,
		DefaultToolsApprovalMode: pluginApprovalPtr(apps.AppToolApprovalWrites),
		EnabledTools:             []string{"read", "search"},
		Tools: map[string]ToolConfig{
			"search": {ApprovalMode: pluginApprovalPtr(apps.AppToolApprovalPrompt)},
		},
	}
}

// Rust #63614: the plugin manifest supplies a `writes` default while the user
// setting `prompt` takes precedence, and the manifest's per-tool override
// still resolves for the tools it names.
func TestPluginMCPServerApprovalPolicyPrecedenceLikeRust(t *testing.T) {
	// Plugin-only registration keeps the manifest policy.
	pluginRuntime := NewManager(nil).RuntimeConfig(RuntimeConfig{}, []ConfigOverlay{{
		Name:              "sample",
		Config:            pluginManifestSamplePolicy(),
		Source:            CatalogSourcePlugin,
		PluginID:          "sample@test",
		ContributionOrder: 1,
	}})
	pluginService := NewMCPService(pluginRuntime)
	if got := pluginService.ToolApprovalMode("sample", "read"); got != apps.AppToolApprovalWrites {
		t.Fatalf("manifest default for read = %q, want writes", got)
	}
	if got := pluginService.ToolApprovalMode("sample", "search"); got != apps.AppToolApprovalPrompt {
		t.Fatalf("manifest per-tool override for search = %q, want prompt", got)
	}

	// The user setting replaces the manifest default and the tool filters.
	base := RuntimeConfigFromValues(map[string]any{"mcp_servers": map[string]any{
		"sample": map[string]any{
			"url":                         "https://sample.example/mcp",
			"default_tools_approval_mode": "prompt",
			"enabled_tools":               []any{"search"},
			"disabled_tools":              []any{"delete"},
		},
	}}, "")
	userRuntime := NewManager(nil).RuntimeConfig(*base, []ConfigOverlay{{
		Name:              "sample",
		Config:            pluginManifestSamplePolicy(),
		Source:            CatalogSourcePlugin,
		PluginID:          "sample@test",
		ContributionOrder: 1,
	}})
	registration := userRuntime.Servers["sample"]
	if source := SourceFromRegistration(&registration); source != CatalogSourceConfig {
		t.Fatalf("winning source = %q, want user config", source)
	}
	userService := NewMCPService(userRuntime)
	if got := userService.ToolApprovalMode("sample", "read"); got != apps.AppToolApprovalPrompt {
		t.Fatalf("user default for read = %q, want prompt", got)
	}
	if got := registration.Config.EnabledTools; len(got) != 1 || got[0] != "search" {
		t.Fatalf("enabled tools = %v, want [search]", got)
	}
	if got := registration.Config.DisabledTools; len(got) != 1 || got[0] != "delete" {
		t.Fatalf("disabled tools = %v, want [delete]", got)
	}
}

// Rust #63614 keeps the regression test that a per-tool override takes effect
// after the plugin policy was already read once (warmed cache).
func TestPluginPerToolApprovalOverrideAppliesAfterConfigRefreshLikeRust(t *testing.T) {
	service := NewMCPService(NewManager(nil).RuntimeConfig(RuntimeConfig{}, []ConfigOverlay{{
		Name:              "sample",
		Config:            pluginManifestSamplePolicy(),
		Source:            CatalogSourcePlugin,
		PluginID:          "sample@test",
		ContributionOrder: 1,
	}}))
	if got := service.ToolApprovalMode("sample", "search"); got != apps.AppToolApprovalPrompt {
		t.Fatalf("initial search mode = %q, want prompt", got)
	}
	updated := pluginManifestSamplePolicy()
	updated.Tools = map[string]ToolConfig{
		"search": {ApprovalMode: pluginApprovalPtr(apps.AppToolApprovalApprove)},
	}
	service.SetServerConfig("sample", &updated)
	if got := service.ToolApprovalMode("sample", "search"); got != apps.AppToolApprovalApprove {
		t.Fatalf("refreshed search mode = %q, want approve", got)
	}
	if got := service.ToolApprovalMode("sample", "read"); got != apps.AppToolApprovalWrites {
		t.Fatalf("refreshed default for read = %q, want writes", got)
	}
}
