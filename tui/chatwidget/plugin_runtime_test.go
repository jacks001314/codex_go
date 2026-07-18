package chatwidget

import (
	"strings"
	"testing"

	pluginapi "codex_go/plugin"
)

func TestPluginsRuntimeFetchLoadAndRemoteSectionsMatchRust(t *testing.T) {
	cwd := `D:\repo`
	localPath := `D:\repo\marketplaces\team`
	state := PluginsRuntimeState{
		CurrentCWD:                     cwd,
		ActiveTabID:                    MarketplaceTabIDPrefix + `D:\repo`,
		NewlyInstalledMarketplaceTabID: MarketplaceTabIDPrefix + `D:\repo`,
	}

	started := state.OnPluginsListFetchStarted(cwd, false)
	if !started.ShowLoadingPopup || state.Fetch.InFlightCWD != cwd || !state.Fetch.VerticalSectionRequested || state.Cache.Kind != PluginsCacheLoading {
		t.Fatalf("fetch started = %#v state=%#v", started, state)
	}

	response := pluginapi.PluginListResponse{Marketplaces: []pluginapi.PluginMarketplaceEntry{{
		Name: "local",
		Path: &localPath,
	}}}
	loaded := state.OnPluginsLoaded(cwd, &response, "", false, false)
	if !loaded.RefreshPopup || state.Cache.Kind != PluginsCacheReady || state.Fetch.CacheCWD != cwd {
		t.Fatalf("loaded = %#v state=%#v", loaded, state)
	}
	if !state.RemoteSectionsLoading || state.RemoteSectionsLoaded {
		t.Fatalf("remote loading flags = loading:%v loaded:%v", state.RemoteSectionsLoading, state.RemoteSectionsLoaded)
	}
	if state.ActiveTabID != MarketplaceTabIDFromPath(localPath) || state.NewlyInstalledMarketplaceTabID != "" {
		t.Fatalf("tab fallback active=%q newly=%q", state.ActiveTabID, state.NewlyInstalledMarketplaceTabID)
	}

	remote := []pluginapi.PluginMarketplaceEntry{{Name: RemoteWorkspaceMarketplace}}
	sectionErrors := []PluginCatalogRemoteSectionError{{SectionID: "shared-with-me", Label: "Shared with me", Message: "offline"}}
	remoteLoaded := state.OnPluginRemoteSectionsLoaded(cwd, remote, sectionErrors, true)
	if !remoteLoaded.RefreshPopup || state.RemoteSectionsLoading || !state.RemoteSectionsLoaded || state.Fetch.VerticalSectionRequested {
		t.Fatalf("remote loaded = %#v state=%#v", remoteLoaded, state)
	}
	if len(state.Cache.Response.Marketplaces) != 2 || state.Cache.Response.Marketplaces[1].Name != RemoteWorkspaceMarketplace {
		t.Fatalf("merged marketplaces = %#v", state.Cache.Response.Marketplaces)
	}
	if len(state.RemoteSectionErrors) != 1 || state.RemoteSectionErrors[0].Message != "offline" {
		t.Fatalf("section errors = %#v", state.RemoteSectionErrors)
	}
}

func TestPluginsRuntimeLoadErrorAndCwdGuardsMatchRust(t *testing.T) {
	state := PluginsRuntimeState{CurrentCWD: `D:\repo`}
	ignored := state.OnPluginsLoaded(`D:\other`, nil, "boom", true, false)
	if !ignored.Ignored || state.Cache.Kind != "" {
		t.Fatalf("ignored = %#v state=%#v", ignored, state)
	}

	state.OnPluginsListFetchStarted(`D:\repo`, true)
	failed := state.OnPluginsLoaded(`D:\repo`, nil, "network down", true, false)
	if !failed.ShowErrorPopup || failed.ErrorMessage != "network down" || state.Cache.Kind != PluginsCacheFailed || state.Fetch.VerticalSectionRequested {
		t.Fatalf("failed = %#v state=%#v", failed, state)
	}
}

func TestPluginsRuntimeMarketplaceResultsMatchRust(t *testing.T) {
	cwd := `D:\repo`
	state := PluginsRuntimeState{CurrentCWD: cwd}

	added := state.OnMarketplaceAddLoaded(cwd, &pluginapi.MarketplaceAddResponse{
		MarketplaceName: "team",
		InstalledRoot:   `D:\repo\marketplaces\team`,
	}, "")
	if added.InfoMessage != "Added marketplace team." || !strings.Contains(added.InfoHint, `D:\repo\marketplaces\team`) ||
		state.ActiveTabID != MarketplaceTabIDFromPath(`D:\repo\marketplaces\team`) ||
		state.NewlyInstalledMarketplaceTabID != state.ActiveTabID {
		t.Fatalf("added = %#v state=%#v", added, state)
	}

	already := state.OnMarketplaceAddLoaded(cwd, &pluginapi.MarketplaceAddResponse{
		MarketplaceName: "team",
		InstalledRoot:   `D:\repo\marketplaces\team`,
		AlreadyAdded:    true,
	}, "")
	if already.InfoMessage != "Marketplace team is already added." || state.NewlyInstalledMarketplaceTabID != "" {
		t.Fatalf("already = %#v state=%#v", already, state)
	}

	root := `D:\repo\marketplaces\team`
	removed := state.OnMarketplaceRemoveLoaded(cwd, "team", "Team", &pluginapi.MarketplaceRemoveResponse{
		MarketplaceName: "team",
		InstalledRoot:   &root,
	}, "")
	if removed.InfoMessage != "Removed marketplace Team." || removed.InfoHint != "Marketplace root: "+root || state.ActiveTabID != AllPluginsTabID {
		t.Fatalf("removed = %#v state=%#v", removed, state)
	}

	upgraded := state.OnMarketplaceUpgradeLoaded(cwd, &pluginapi.MarketplaceUpgradeResponse{
		SelectedMarketplaces: []string{"team", "docs"},
		UpgradedRoots:        []string{`D:\repo\marketplaces\team`},
		Errors:               []pluginapi.MarketplaceUpgradeErrorInfo{{MarketplaceName: "docs", Message: "fetch failed"}},
	}, "")
	if upgraded.InfoMessage != "Upgraded 1 marketplace." || !strings.Contains(upgraded.ErrorMessage, "docs: fetch failed") ||
		state.ActiveTabID != MarketplaceTabIDFromPath(`D:\repo\marketplaces\team`) {
		t.Fatalf("upgraded = %#v state=%#v", upgraded, state)
	}

	none := state.OnMarketplaceUpgradeLoaded(cwd, &pluginapi.MarketplaceUpgradeResponse{}, "")
	if none.InfoMessage != "No configured Git marketplaces to upgrade." {
		t.Fatalf("none = %#v", none)
	}
}

func TestPluginsRuntimePluginEnabledUpdatesCacheMatchRust(t *testing.T) {
	cwd := `D:\repo`
	state := PluginsRuntimeState{CurrentCWD: cwd}
	response := pluginapi.PluginListResponse{Marketplaces: []pluginapi.PluginMarketplaceEntry{{
		Name: "local",
		Plugins: []pluginapi.PluginSummary{{
			ID:      "docs",
			Enabled: false,
		}},
	}}}
	state.OpenPluginsList(cwd, response, "")

	updated := state.OnPluginEnabledSet(cwd, "docs", true, "")
	if !updated.RefreshPopup || !state.Cache.Response.Marketplaces[0].Plugins[0].Enabled {
		t.Fatalf("updated = %#v state=%#v", updated, state)
	}

	failed := state.OnPluginEnabledSet(cwd, "docs", false, "locked")
	if !failed.RefreshPopup || failed.ErrorMessage != "Failed to update plugin config for docs: locked" || !state.Cache.Response.Marketplaces[0].Plugins[0].Enabled {
		t.Fatalf("failed = %#v state=%#v", failed, state)
	}
}

func TestPluginsRuntimePopupKeyEventMatchesRust(t *testing.T) {
	cwd := `D:\repo`
	localPath := `D:\repo\marketplaces\team`
	state := PluginsRuntimeState{CurrentCWD: cwd}
	state.OpenPluginsList(cwd, pluginapi.PluginListResponse{Marketplaces: []pluginapi.PluginMarketplaceEntry{{
		Name:      "team",
		Path:      &localPath,
		Interface: pluginapi.MarketplaceInterface{DisplayName: stringPtrRuntimeTest("Team Plugins")},
	}}}, "")

	activeTab := MarketplaceTabIDFromPath(localPath)
	remove := state.HandlePluginsPopupKeyEvent(activeTab, PluginsPopupKeyEvent{CtrlR: true, Press: true}, map[string]bool{"team": true}, nil)
	if !remove.Handled || !remove.OpenRemoveConfirmation || remove.MarketplaceName != "team" || remove.MarketplaceDisplayName != "Team Plugins" {
		t.Fatalf("remove decision = %#v", remove)
	}

	keyRelease := state.HandlePluginsPopupKeyEvent(activeTab, PluginsPopupKeyEvent{CtrlU: true}, map[string]bool{"team": true}, map[string]bool{"team": true})
	if !keyRelease.Handled || keyRelease.OpenMarketplaceUpgrade || keyRelease.FetchMarketplaceUpgrade {
		t.Fatalf("upgrade key release decision = %#v", keyRelease)
	}

	upgrade := state.HandlePluginsPopupKeyEvent(activeTab, PluginsPopupKeyEvent{CtrlU: true, Press: true}, map[string]bool{"team": true}, map[string]bool{"team": true})
	if !upgrade.Handled || !upgrade.OpenMarketplaceUpgrade || !upgrade.FetchMarketplaceUpgrade || upgrade.MarketplaceName != "team" {
		t.Fatalf("upgrade decision = %#v", upgrade)
	}

	blocked := state.HandlePluginsPopupKeyEvent(activeTab, PluginsPopupKeyEvent{CtrlU: true, Press: true}, map[string]bool{"team": true}, map[string]bool{"team": false})
	if blocked.Handled {
		t.Fatalf("blocked upgrade should not handle: %#v", blocked)
	}
}

func TestPluginsRuntimeInstallAuthFlowMatchRust(t *testing.T) {
	cwd := `D:\repo`
	url := "https://chatgpt.com/apps/docs"
	state := PluginsRuntimeState{CurrentCWD: cwd, Cache: NewPluginsCacheReady(pluginapi.PluginListResponse{}), Fetch: PluginListFetchState{CacheCWD: cwd}}
	installed := state.OnPluginInstallLoaded(cwd, "Docs", &pluginapi.PluginInstallResponse{
		AppsNeedingAuth: []pluginapi.AppSummary{
			{ID: "drive", Name: "Drive", InstallURL: &url},
			{ID: "calendar", Name: "Calendar"},
		},
	}, "")
	if !installed.OpenAuthPopup || installed.InfoMessage != "Installed Docs plugin." || !strings.Contains(installed.InfoHint, "2 app(s)") {
		t.Fatalf("installed = %#v state=%#v", installed, state)
	}

	view, ok := state.CurrentPluginInstallAuthView(map[string]bool{"drive": true})
	if !ok || view.Items[1].Name != "Manage on ChatGPT" || view.Items[1].SelectedDescription != "Open the app page in your browser." ||
		view.Items[2].Name != "Continue" || view.Items[2].SelectedDescription != "Advance to the next app." {
		t.Fatalf("auth view = %#v ok=%v", view, ok)
	}

	next := state.AdvancePluginInstallAuthFlow()
	if !next.OpenAuthPopup || state.PluginInstallAuthFlow.NextAppIndex != 1 {
		t.Fatalf("advance = %#v state=%#v", next, state)
	}
	view, ok = state.CurrentPluginInstallAuthView(nil)
	if !ok || view.Items[1].Name != "ChatGPT apps link unavailable" || view.Items[2].Name != "I've installed it" ||
		view.Items[2].SelectedDescription != "Continue without waiting for refresh to complete." {
		t.Fatalf("second auth view = %#v ok=%v", view, ok)
	}

	done := state.AdvancePluginInstallAuthFlow()
	if !done.FinishedAuthFlow || !done.RefreshPopup || done.InfoMessage != "Completed app setup flow for Docs plugin." ||
		state.PluginInstallAuthFlow != nil || len(state.PluginInstallAppsNeedingAuth) != 0 {
		t.Fatalf("done = %#v state=%#v", done, state)
	}

	plain := state.OnPluginInstallLoaded(cwd, "Docs", &pluginapi.PluginInstallResponse{}, "")
	if plain.InfoHint != "No additional app authentication is required." || plain.OpenAuthPopup {
		t.Fatalf("plain install = %#v", plain)
	}

	uninstalled := state.OnPluginUninstallLoaded(cwd, "Docs", "")
	if uninstalled.InfoMessage != "Uninstalled Docs plugin." || uninstalled.InfoHint != "Bundled apps remain installed." {
		t.Fatalf("uninstalled = %#v", uninstalled)
	}
}

func stringPtrRuntimeTest(value string) *string {
	return &value
}
