package app

import (
	"reflect"
	"testing"

	"codex_go/plugin"
)

func TestPluginMentionsUsePluginListSummariesAndGUIEligibilityMatchRust(t *testing.T) {
	active := pluginMentionSummary("active")
	activeShared := pluginMentionSummary("active-shared")
	activeShared.RemotePluginID = "plugins~active-shared"
	disabledByAdmin := pluginMentionSummary("disabled-by-admin")
	disabledByAdmin.Availability = plugin.PluginDisabledByAdmin
	disabled := pluginMentionSummary("disabled")
	disabled.Enabled = false
	uninstalled := pluginMentionSummary("uninstalled")
	uninstalled.Installed = false

	response := plugin.PluginListResponse{
		Marketplaces: []plugin.PluginMarketplaceEntry{{
			Name: "server-marketplace",
			Plugins: []plugin.PluginSummary{
				active,
				activeShared,
				disabledByAdmin,
				disabled,
				uninstalled,
			},
		}},
		Plugins: []plugin.PluginSummary{pluginMentionSummary("top-level-is-ignored")},
	}

	got := PluginMentionsFromListResponse(response)
	want := []plugin.CapabilitySummary{
		{
			Name:           "active",
			ConfigName:     "active@server-marketplace",
			DisplayName:    "active",
			RemotePluginID: "plugins~active",
			Description:    "server-marketplace",
			MCPServers:     []string{},
			AppConnectors:  []string{},
		},
		{
			Name:           "active-shared",
			ConfigName:     "active-shared@server-marketplace",
			DisplayName:    "active-shared",
			RemotePluginID: "plugins~active-shared",
			Description:    "server-marketplace",
			MCPServers:     []string{},
			AppConnectors:  []string{},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PluginMentionsFromListResponse() = %#v, want %#v", got, want)
	}
}

func TestPluginMentionDisplayAndDescriptionMatchRust(t *testing.T) {
	displayName := "  Docs Search  "
	shortDescription := "  Search project docs  "
	summary := pluginMentionSummary("docs")
	summary.Interface = &plugin.PluginInterface{
		DisplayName:      &displayName,
		ShortDescription: &shortDescription,
	}

	mention, ok := PluginMentionFromSummary(" marketplace ", summary)
	if !ok {
		t.Fatal("PluginMentionFromSummary() ok = false, want true")
	}
	if mention.DisplayName != "Docs Search" {
		t.Fatalf("display name = %q, want Docs Search", mention.DisplayName)
	}
	if mention.Description != "Search project docs" {
		t.Fatalf("description = %q, want Search project docs", mention.Description)
	}

	blank := "   "
	summary.Interface.DisplayName = &blank
	summary.Interface.ShortDescription = &blank
	mention, ok = PluginMentionFromSummary(" marketplace ", summary)
	if !ok {
		t.Fatal("PluginMentionFromSummary(blank) ok = false, want true")
	}
	if mention.DisplayName != "docs" {
		t.Fatalf("blank display name fallback = %q, want docs", mention.DisplayName)
	}
	if mention.Description != "marketplace" {
		t.Fatalf("blank description fallback = %q, want marketplace", mention.Description)
	}
}

func TestPluginMentionDescriptionCanBeEmptyMatchRust(t *testing.T) {
	summary := pluginMentionSummary("docs")
	mention, ok := PluginMentionFromSummary("   ", summary)
	if !ok {
		t.Fatal("PluginMentionFromSummary() ok = false, want true")
	}
	if mention.Description != "" {
		t.Fatalf("description = %q, want empty", mention.Description)
	}
}

func pluginMentionSummary(name string) plugin.PluginSummary {
	return plugin.PluginSummary{
		ID:             name + "@server-marketplace",
		RemotePluginID: "plugins~" + name,
		Name:           name,
		Availability:   plugin.PluginAvailable,
		Installed:      true,
		Enabled:        true,
	}
}
