package app

import (
	"reflect"
	"testing"

	"codex_go/plugin"
)

// TestPluginMentionsUseInstalledResponseLikeRust covers Rust #57855: mention
// candidates come from `plugin/installed` marketplace entries, keep only
// installed, enabled, non-admin-disabled plugins, and ignore the response's
// flattened plugin list.
func TestPluginMentionsUseInstalledResponseLikeRust(t *testing.T) {
	active := pluginMentionSummary("active")
	shared := pluginMentionSummary("active-shared")
	disabledByAdmin := pluginMentionSummary("disabled-by-admin")
	disabledByAdmin.Availability = plugin.PluginDisabledByAdmin
	disabled := pluginMentionSummary("disabled")
	disabled.Enabled = false
	uninstalled := pluginMentionSummary("uninstalled")
	uninstalled.Installed = false

	response := plugin.PluginInstalledResponse{
		Marketplaces: []plugin.PluginMarketplaceEntry{{
			Name:    "server-marketplace",
			Plugins: []plugin.PluginSummary{active, shared, disabledByAdmin, disabled, uninstalled},
		}},
		Plugins: []plugin.PluginSummary{pluginMentionSummary("flattened-is-ignored")},
	}

	got := PluginMentionsFromInstalledResponse(response)
	wantNames := []string{"active", "active-shared"}
	if len(got) != len(wantNames) {
		t.Fatalf("mentions = %#v, want %d entries", got, len(wantNames))
	}
	for i, name := range wantNames {
		if got[i].Name != name || got[i].ConfigName != name+"@server-marketplace" {
			t.Fatalf("mention %d = %#v, want %q", i, got[i], name)
		}
		if !reflect.DeepEqual(got[i].MCPServers, []string{}) || !reflect.DeepEqual(got[i].AppConnectors, []string{}) {
			t.Fatalf("mention %d capability summaries = %#v", i, got[i])
		}
	}
}
