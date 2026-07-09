package chatwidget

import (
	"reflect"
	"testing"

	pluginapi "codex_go/internal/plugin"
)

func TestPluginCatalogTabsRemoteFallbacksMatchRust(t *testing.T) {
	loading := PluginCatalogTabs(pluginapi.PluginListResponse{}, PluginCatalogTabsOptions{
		RemoteSectionsLoading: true,
	})
	if got := pluginCatalogTabIDs(loading); !reflect.DeepEqual(got, []string{
		RemoteLoadingTabIDPrefix + "workspace-loading",
		RemoteLoadingTabIDPrefix + "shared-with-me-loading",
	}) {
		t.Fatalf("loading tabs = %#v", got)
	}
	if loading[0].Item.Name != "Loading workspace plugins..." || !loading[0].Item.Disabled {
		t.Fatalf("workspace loading item = %#v", loading[0].Item)
	}

	loaded := PluginCatalogTabs(pluginapi.PluginListResponse{}, PluginCatalogTabsOptions{
		RemoteSectionsLoaded: true,
	})
	if got := pluginCatalogTabIDs(loaded); !reflect.DeepEqual(got, []string{RemoteEmptyTabIDPrefix + "workspace"}) {
		t.Fatalf("loaded empty tabs = %#v", got)
	}
	if loaded[0].Item.Name != "No workspace plugins available" {
		t.Fatalf("empty item = %#v", loaded[0].Item)
	}

	withError := PluginCatalogTabs(pluginapi.PluginListResponse{}, PluginCatalogTabsOptions{
		RemoteSectionsLoaded: true,
		SectionErrors:        []PluginCatalogRemoteSectionError{{SectionID: "shared-with-me", Message: "offline"}},
	})
	if got := pluginCatalogTabIDs(withError); !reflect.DeepEqual(got, []string{
		RemoteEmptyTabIDPrefix + "workspace",
		RemoteErrorTabIDPrefix + "shared-with-me",
	}) {
		t.Fatalf("error tabs = %#v", got)
	}
	if withError[1].Item.Description != "offline" {
		t.Fatalf("error item = %#v", withError[1].Item)
	}
}

func TestPluginCatalogTabsSkipFallbackWhenMarketplacePresentAndSavedIDFallsBack(t *testing.T) {
	response := pluginapi.PluginListResponse{Marketplaces: []pluginapi.PluginMarketplaceEntry{
		{Name: RemoteWorkspaceMarketplace},
	}}
	tabs := PluginCatalogTabs(response, PluginCatalogTabsOptions{RemoteSectionsLoading: true})
	if got := pluginCatalogTabIDs(tabs); !reflect.DeepEqual(got, []string{
		MarketplaceTabIDPrefix + RemoteWorkspaceMarketplace,
		RemoteLoadingTabIDPrefix + "shared-with-me-loading",
	}) {
		t.Fatalf("tabs = %#v", got)
	}

	if got, ok := PluginCatalogTabMatchingSavedID(MarketplaceTabIDPrefix+RemoteWorkspaceSharedWithMe, tabs); !ok || got != RemoteLoadingTabIDPrefix+"shared-with-me-loading" {
		t.Fatalf("saved shared tab fallback = %q/%v", got, ok)
	}
	if got, ok := PluginCatalogTabMatchingSavedID(MarketplaceTabIDPrefix+RemoteWorkspaceMarketplace, tabs); !ok || got != MarketplaceTabIDPrefix+RemoteWorkspaceMarketplace {
		t.Fatalf("saved workspace tab = %q/%v", got, ok)
	}
}

func TestPluginCatalogTabsOrderAndChromeTabsMatchRust(t *testing.T) {
	localPath := `C:\repo\.agents\plugins\marketplace.json`
	response := pluginapi.PluginListResponse{Marketplaces: []pluginapi.PluginMarketplaceEntry{
		{Name: OpenAICuratedMarketplaceName},
		{Name: "local", Path: &localPath},
		{Name: RemoteWorkspaceSharedPrivate},
		{Name: RemoteWorkspaceMarketplace},
	}}
	tabs := PluginCatalogTabs(response, PluginCatalogTabsOptions{
		IncludeAll:            true,
		IncludeInstalled:      true,
		IncludeAddMarketplace: true,
	})

	if got := pluginCatalogTabIDs(tabs); !reflect.DeepEqual(got, []string{
		AllPluginsTabID,
		InstalledPluginsTabID,
		MarketplaceTabIDPrefix + RemoteWorkspaceMarketplace,
		MarketplaceTabIDPrefix + RemoteWorkspaceSharedPrivate,
		MarketplaceTabIDPrefix + localPath,
		MarketplaceTabIDPrefix + OpenAICuratedMarketplaceName,
		AddMarketplaceTabID,
	}) {
		t.Fatalf("ordered tabs = %#v", got)
	}
	if tabs[2].Label != "Workspace" || tabs[3].Label != "Shared with me" || tabs[4].Label != "Local" || tabs[5].Label != "OpenAI Curated" {
		t.Fatalf("labels = %#v", tabs)
	}
}

func TestPluginCatalogDedupePrefersInstalledAdminAndLocalSharesMatchRust(t *testing.T) {
	localPath := `C:\repo\.agents\plugins\marketplace.json`
	display := "Shared Plugin"
	marketplaces := []pluginapi.PluginMarketplaceEntry{
		{
			Name: RemoteWorkspaceSharedWithMe,
			Plugins: []pluginapi.PluginSummary{{
				ID:             "remote@shared",
				Name:           "remote-shared",
				RemotePluginID: "shared-1",
				Interface:      &pluginapi.PluginInterface{DisplayName: &display},
				Source:         pluginapi.PluginSource{Type: "remote"},
				InstallPolicy:  pluginapi.InstallAllowed,
			}},
		},
		{
			Name: "local",
			Path: &localPath,
			Plugins: []pluginapi.PluginSummary{{
				ID:            "local@local",
				Name:          "local-shared",
				Interface:     &pluginapi.PluginInterface{DisplayName: &display},
				Source:        pluginapi.PluginSource{Type: "local", Path: `plugins\local-shared`},
				ShareContext:  &pluginapi.PluginShareContext{RemotePluginID: "shared-1"},
				InstallPolicy: pluginapi.InstallAllowed,
			}},
		},
	}

	entries := PluginEntriesForMarketplaces(marketplaces)
	if len(entries) != 1 || entries[0].Plugin.ID != "local@local" {
		t.Fatalf("local shared plugin should replace remote duplicate: %#v", entries)
	}

	installedRemote := marketplaces
	installedRemote[0].Plugins[0].Installed = true
	entries = PluginEntriesForMarketplaces(installedRemote)
	if len(entries) != 1 || entries[0].Plugin.ID != "remote@shared" {
		t.Fatalf("installed remote should win: %#v", entries)
	}

	adminLocal := marketplaces
	adminLocal[0].Plugins[0].Installed = false
	adminLocal[1].Plugins[0].Installed = false
	adminLocal[0].Plugins[0].InstallPolicy = pluginapi.InstallAllowed
	adminLocal[1].Plugins[0].InstallPolicy = pluginapi.InstallInstalledByDefault
	entries = PluginEntriesForMarketplaces(adminLocal)
	if len(entries) != 1 || entries[0].Plugin.ID != "local@local" {
		t.Fatalf("admin managed local should win: %#v", entries)
	}
}

func TestPluginCatalogDetailRequestPrefersMatchingLocalSourceMatchRust(t *testing.T) {
	localPath := `C:\repo\.agents\plugins\marketplace.json`
	localMarketplace := pluginapi.PluginMarketplaceEntry{
		Name: "local",
		Path: &localPath,
		Plugins: []pluginapi.PluginSummary{{
			ID:            "local@local",
			Name:          "local-shared",
			Source:        pluginapi.PluginSource{Type: "local", Path: `plugins\local-shared`},
			ShareContext:  &pluginapi.PluginShareContext{RemotePluginID: "shared-1"},
			Installed:     true,
			InstallPolicy: pluginapi.InstallAllowed,
		}},
	}
	remoteMarketplace := pluginapi.PluginMarketplaceEntry{
		Name: RemoteWorkspaceSharedWithMe,
		Plugins: []pluginapi.PluginSummary{{
			ID:             "remote@shared",
			Name:           "remote-shared",
			RemotePluginID: "shared-1",
			Source:         pluginapi.PluginSource{Type: "remote"},
			Installed:      true,
			InstallPolicy:  pluginapi.InstallAllowed,
		}},
	}
	preferred := PreferredLocalPluginSources([]pluginapi.PluginMarketplaceEntry{localMarketplace, remoteMarketplace})
	request, ok := PluginDetailRequestForEntry(remoteMarketplace, remoteMarketplace.Plugins[0], preferred)
	if !ok || request.Location.Kind != PluginLocationLocal || request.Location.MarketplacePath != localPath || request.PluginName != "local-shared" {
		t.Fatalf("preferred local detail request = %#v ok=%v", request, ok)
	}
	if request.ReadParams.MarketplacePath != localPath || request.ReadParams.PluginName != "local-shared" || request.ReadParams.RemotePluginID != "shared-1" {
		t.Fatalf("read params = %#v", request.ReadParams)
	}

	remoteMarketplace.Plugins[0].Installed = false
	request, ok = PluginDetailRequestForEntry(remoteMarketplace, remoteMarketplace.Plugins[0], preferred)
	if !ok || request.Location.Kind != PluginLocationRemote || request.Location.MarketplaceName != RemoteWorkspaceSharedWithMe || request.PluginName != "shared-1" {
		t.Fatalf("remote detail request = %#v ok=%v", request, ok)
	}

	if uninstallID, ok := PluginUninstallID(remoteMarketplace.Plugins[0]); !ok || uninstallID != "shared-1" {
		t.Fatalf("remote uninstall id = %q/%v", uninstallID, ok)
	}
	if uninstallID, ok := PluginUninstallID(localMarketplace.Plugins[0]); !ok || uninstallID != "local@local" {
		t.Fatalf("local uninstall id = %q/%v", uninstallID, ok)
	}
}

func pluginCatalogTabIDs(tabs []PluginCatalogTab) []string {
	out := make([]string, len(tabs))
	for i := range tabs {
		out[i] = tabs[i].ID
	}
	return out
}
