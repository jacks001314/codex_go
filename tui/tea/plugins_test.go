package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/plugin"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

func TestPluginBrowserSearchToggleAndDetailFlow(t *testing.T) {
	marketplacePath := `D:\plugins\team`
	display := "Docs"
	short := "Search docs."
	response := pluginBrowserTestResponse(marketplacePath, display, short, true, true)
	var readParams plugin.PluginReadParams
	var toggledID string
	var toggledEnabled bool
	model := NewModel(codextui.NewState(nil), Options{
		Width:            100,
		Height:           28,
		SessionPickerCWD: `D:\repo`,
		OnReadPlugins: func(cwd string, forceRefetch bool) (plugin.PluginListResponse, error) {
			if cwd != `D:\repo` || forceRefetch {
				t.Fatalf("list got cwd=%q force=%v", cwd, forceRefetch)
			}
			return response, nil
		},
		OnReadPlugin: func(params plugin.PluginReadParams) (plugin.PluginReadResponse, error) {
			readParams = params
			return plugin.PluginReadResponse{Plugin: pluginBrowserTestDetail(response)}, nil
		},
		OnWritePluginEnabled: func(pluginID string, enabled bool) error {
			toggledID = pluginID
			toggledEnabled = enabled
			return nil
		},
	})

	runTeaCmd(t, model, model.applyPluginsCommand())
	state := model.pluginBrowserState()
	if state == nil || len(state.catalog.Tabs) < 4 {
		t.Fatalf("plugin catalog did not open: %#v", state)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("doc")})
	if got := len(state.filteredItems()); got != 1 {
		t.Fatalf("filtered items = %d, want 1", got)
	}

	_, cmd := model.Update(key(bubbletea.KeySpace))
	if cmd == nil {
		t.Fatal("space did not start plugin toggle")
	}
	runTeaCmd(t, model, cmd)
	if toggledID != "docs@team" || toggledEnabled {
		t.Fatalf("toggle = %q/%v, want docs@team/false", toggledID, toggledEnabled)
	}
	if !strings.Contains(model.View(), "[ ] Docs") {
		t.Fatalf("successful toggle was not reflected in catalog:\n%s", model.View())
	}

	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("enter did not start plugin/read")
	}
	runTeaCmd(t, model, cmd)
	if readParams.MarketplacePath != marketplacePath || readParams.PluginName != "docs" {
		t.Fatalf("plugin/read params = %#v", readParams)
	}
	view := model.View()
	for _, want := range []string{"Back to plugins", "Uninstall plugin", "Search docs."} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail view missing %q:\n%s", want, view)
		}
	}
}

func TestPluginBrowserToggleFailureUsesRustMessage(t *testing.T) {
	response := pluginBrowserTestResponse(`D:\plugins\team`, "Docs", "Search docs.", true, true)
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 28,
		OnWritePluginEnabled: func(pluginID string, enabled bool) error {
			return errors.New("permission denied")
		},
	})
	model.openPluginBrowser("", response, chatwidget.AllPluginsTabID)
	_, cmd := model.Update(key(bubbletea.KeySpace))
	runTeaCmd(t, model, cmd)
	want := "Failed to update plugin config for docs@team: permission denied"
	if !strings.Contains(model.View(), want) {
		t.Fatalf("toggle error missing %q:\n%s", want, model.View())
	}
}

func TestPluginBrowserCoalescesRapidToggleWrites(t *testing.T) {
	response := pluginBrowserTestResponse(`D:\plugins\team`, "Docs", "Search docs.", true, true)
	writes := make(chan bool, 2)
	releases := make(chan struct{}, 2)
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 28,
		OnWritePluginEnabled: func(pluginID string, enabled bool) error {
			writes <- enabled
			<-releases
			return nil
		},
	})
	model.openPluginBrowser("", response, chatwidget.AllPluginsTabID)
	_, first := model.Update(key(bubbletea.KeySpace))
	if first == nil {
		t.Fatal("first toggle returned no command")
	}
	_, second := model.Update(key(bubbletea.KeySpace))
	if second != nil {
		t.Fatal("second toggle started a concurrent write")
	}

	firstResults := make(chan bubbletea.Msg, 1)
	go func() { firstResults <- first() }()
	if enabled := <-writes; enabled {
		t.Fatalf("first write enabled = %v, want false", enabled)
	}
	releases <- struct{}{}
	_, followup := model.Update(<-firstResults)
	if followup == nil {
		t.Fatal("coalesced final state did not start a follow-up write")
	}
	followupResults := make(chan bubbletea.Msg, 1)
	go func() { followupResults <- followup() }()
	if enabled := <-writes; !enabled {
		t.Fatalf("follow-up write enabled = %v, want true", enabled)
	}
	releases <- struct{}{}
	model.Update(<-followupResults)
	if _, ok := model.pluginToggleDesired["docs@team"]; ok {
		t.Fatalf("toggle desired state was not cleared: %#v", model.pluginToggleDesired)
	}
}

func TestPluginBrowserInstallAndUninstallUseRustHistory(t *testing.T) {
	marketplacePath := `D:\plugins\team`
	response := pluginBrowserTestResponse(marketplacePath, "Docs", "Search docs.", false, false)
	installedResponse := pluginBrowserTestResponse(marketplacePath, "Docs", "Search docs.", true, true)
	listCalls := 0
	installCalls := 0
	uninstallCalls := 0
	model := NewModel(codextui.NewState(nil), Options{
		Width:                  100,
		Height:                 28,
		PluginUserMarketplaces: map[string]bool{"team": true},
		PluginGitMarketplaces:  map[string]bool{"team": true},
		OnReadPlugins: func(cwd string, forceRefetch bool) (plugin.PluginListResponse, error) {
			listCalls++
			if !forceRefetch {
				return response, nil
			}
			return installedResponse, nil
		},
		OnReadPlugin: func(params plugin.PluginReadParams) (plugin.PluginReadResponse, error) {
			return plugin.PluginReadResponse{Plugin: pluginBrowserTestDetail(response)}, nil
		},
		OnInstallPlugin: func(params plugin.PluginInstallParams) (plugin.PluginInstallResponse, error) {
			installCalls++
			if params.MarketplacePath != marketplacePath || params.PluginName != "docs" {
				t.Fatalf("install params = %#v", params)
			}
			return plugin.PluginInstallResponse{}, nil
		},
		OnUninstallPlugin: func(params plugin.PluginUninstallParams) (plugin.PluginUninstallResponse, error) {
			uninstallCalls++
			if params.PluginID != "docs@team" {
				t.Fatalf("uninstall params = %#v", params)
			}
			return plugin.PluginUninstallResponse{}, nil
		},
	})

	runTeaCmd(t, model, model.applyPluginsCommand())
	runTeaCmd(t, model, mustPluginCmd(t, model.openSelectedPlugin(), "plugin/read"))
	selectPluginViewAction(t, model, chatwidget.PluginMenuActionInstall)
	installCmd := mustPluginCmd(t, model.applyPluginSelectionAction(), "plugin/install")
	installResult := installCmd()
	_, refreshCmd := model.Update(installResult)
	runTeaCmd(t, model, refreshCmd)
	if installCalls != 1 || listCalls != 2 {
		t.Fatalf("install/list calls = %d/%d", installCalls, listCalls)
	}
	view := model.View()
	if !strings.Contains(view, "Installed Docs plugin.") || !strings.Contains(view, "No additional app authentication is required.") {
		t.Fatalf("install history missing:\n%s", view)
	}

	model.applyPluginReadResult(PluginReadResultMsg{Response: plugin.PluginReadResponse{Plugin: pluginBrowserTestDetail(installedResponse)}})
	selectPluginViewAction(t, model, chatwidget.PluginMenuActionUninstall)
	uninstallCmd := mustPluginCmd(t, model.applyPluginSelectionAction(), "plugin/uninstall")
	uninstallResult := uninstallCmd()
	_, refreshCmd = model.Update(uninstallResult)
	runTeaCmd(t, model, refreshCmd)
	if uninstallCalls != 1 {
		t.Fatalf("uninstall calls = %d", uninstallCalls)
	}
	view = model.View()
	if !strings.Contains(view, "Uninstalled Docs plugin.") || !strings.Contains(view, "Bundled apps remain installed.") {
		t.Fatalf("uninstall history missing:\n%s", view)
	}
}

func TestPluginBrowserMarketplaceActionsAndAuthFlow(t *testing.T) {
	marketplacePath := `D:\plugins\team`
	response := pluginBrowserTestResponse(marketplacePath, "Docs", "Search docs.", true, true)
	addCalls := 0
	removeCalls := 0
	upgradeCalls := 0
	openedURL := ""
	model := NewModel(codextui.NewState(nil), Options{
		Width:                  100,
		Height:                 28,
		PluginUserMarketplaces: map[string]bool{"team": true},
		PluginGitMarketplaces:  map[string]bool{"team": true},
		OnReadPlugins: func(cwd string, forceRefetch bool) (plugin.PluginListResponse, error) {
			return response, nil
		},
		OnAddMarketplace: func(params plugin.MarketplaceAddParams) (plugin.MarketplaceAddResponse, error) {
			addCalls++
			if params.Source != "owner/repo" {
				t.Fatalf("add params = %#v", params)
			}
			return plugin.MarketplaceAddResponse{MarketplaceName: "team", InstalledRoot: marketplacePath}, nil
		},
		OnRemoveMarketplace: func(params plugin.MarketplaceRemoveParams) (plugin.MarketplaceRemoveResponse, error) {
			removeCalls++
			return plugin.MarketplaceRemoveResponse{MarketplaceName: params.MarketplaceName}, nil
		},
		OnUpgradeMarketplace: func(params plugin.MarketplaceUpgradeParams) (plugin.MarketplaceUpgradeResponse, error) {
			upgradeCalls++
			if params.MarketplaceName == nil || *params.MarketplaceName != "team" {
				t.Fatalf("upgrade params = %#v", params)
			}
			return plugin.MarketplaceUpgradeResponse{SelectedMarketplaces: []string{"team"}, UpgradedRoots: []string{marketplacePath}}, nil
		},
		OnOpenPluginURL: func(target string) error {
			openedURL = target
			return nil
		},
	})
	model.openPluginBrowser("", response, chatwidget.AddMarketplaceTabID)
	_, _ = model.Update(key(bubbletea.KeyEnter))
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("owner/repo")})
	_, addCmd := model.Update(key(bubbletea.KeyEnter))
	addResult := mustPluginCmd(t, addCmd, "marketplace/add")()
	_, refreshCmd := model.Update(addResult)
	runTeaCmd(t, model, refreshCmd)
	if addCalls != 1 || !strings.Contains(model.View(), "Added marketplace team.") {
		t.Fatalf("marketplace add did not complete:\n%s", model.View())
	}
	if got := model.pluginBrowserState().activeTabID(); got != chatwidget.MarketplaceTabID(response.Marketplaces[0]) {
		t.Fatalf("active tab after add = %q", got)
	}

	state := model.pluginBrowserState()
	state.selectTab(chatwidget.MarketplaceTabID(response.Marketplaces[0]))
	_, upgradeCmd := model.Update(key(bubbletea.KeyCtrlU))
	upgradeResult := mustPluginCmd(t, upgradeCmd, "marketplace/upgrade")()
	_, refreshCmd = model.Update(upgradeResult)
	runTeaCmd(t, model, refreshCmd)
	if upgradeCalls != 1 || !strings.Contains(model.View(), "Upgraded 1 marketplace.") {
		t.Fatalf("marketplace upgrade did not complete:\n%s", model.View())
	}

	state = model.pluginBrowserState()
	state.selectTab(chatwidget.MarketplaceTabID(response.Marketplaces[0]))
	model.Update(key(bubbletea.KeyCtrlR))
	selectPluginViewAction(t, model, chatwidget.PluginMenuActionRemoveMarketplace)
	removeCmd := mustPluginCmd(t, model.applyPluginSelectionAction(), "marketplace/remove")
	removeResult := removeCmd()
	_, refreshCmd = model.Update(removeResult)
	runTeaCmd(t, model, refreshCmd)
	if removeCalls != 1 || !strings.Contains(model.View(), "Removed marketplace team.") {
		t.Fatalf("marketplace remove did not complete:\n%s", model.View())
	}
	if got := model.pluginBrowserState().activeTabID(); got != chatwidget.AllPluginsTabID {
		t.Fatalf("active tab after remove = %q", got)
	}

	model.openPluginBrowser("", response, chatwidget.AllPluginsTabID)
	installURL := "https://example.test/install"
	installResult := PluginInstallResultMsg{
		DisplayName: "Docs",
		Response: plugin.PluginInstallResponse{AppsNeedingAuth: []plugin.AppSummary{{
			ID: "drive", Name: "Drive", InstallURL: &installURL,
		}}},
	}
	model.applyPluginInstallResult(installResult)
	if !strings.Contains(model.View(), "1 app(s) still need authentication: Drive") {
		t.Fatalf("auth history missing:\n%s", model.View())
	}
	selectPluginViewAction(t, model, chatwidget.PluginMenuActionOpenAppInstallURL)
	runTeaCmd(t, model, mustPluginCmd(t, model.applyPluginSelectionAction(), "open auth URL"))
	if openedURL != installURL {
		t.Fatalf("opened URL = %q", openedURL)
	}
	selectPluginViewAction(t, model, chatwidget.PluginMenuActionAuthFlowAdvance)
	refreshCmd = model.applyPluginSelectionAction()
	runTeaCmd(t, model, refreshCmd)
	if !strings.Contains(model.View(), "Completed app setup flow for Docs plugin.") {
		t.Fatalf("auth completion history missing:\n%s", model.View())
	}
}

func TestPluginBrowserOnlyOffersRustMarketplaceActionsForUserConfig(t *testing.T) {
	marketplacePath := `D:\plugins\local-team`
	response := pluginBrowserTestResponse(marketplacePath, "Docs", "Search docs.", true, true)
	model := NewModel(codextui.NewState(nil), Options{
		Width:                  80,
		Height:                 24,
		PluginUserMarketplaces: map[string]bool{"team": true},
		PluginGitMarketplaces:  map[string]bool{"team": false},
		OnUpgradeMarketplace: func(params plugin.MarketplaceUpgradeParams) (plugin.MarketplaceUpgradeResponse, error) {
			t.Fatal("local marketplace must not be upgraded")
			return plugin.MarketplaceUpgradeResponse{}, nil
		},
	})
	model.openPluginBrowser("", response, chatwidget.MarketplaceTabID(response.Marketplaces[0]))
	if strings.Contains(model.View(), "ctrl + u upgrade") {
		t.Fatalf("local marketplace displayed upgrade action:\n%s", model.View())
	}
	_, cmd := model.Update(key(bubbletea.KeyCtrlU))
	if cmd != nil {
		t.Fatal("Ctrl+U started an upgrade for a local marketplace")
	}
	model.Update(key(bubbletea.KeyCtrlR))
	if state := model.pluginBrowserState(); state == nil || state.view == nil {
		t.Fatal("configured local marketplace did not open remove confirmation")
	}
}

func TestPluginsCommandDisabledUsesRustHistory(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:           80,
		Height:          24,
		FeatureSettings: map[string]bool{"plugins": false},
		OnReadPlugins: func(cwd string, forceRefetch bool) (plugin.PluginListResponse, error) {
			t.Fatal("disabled /plugins must not read the catalog")
			return plugin.PluginListResponse{}, nil
		},
	})
	if cmd := model.applyPluginsCommand(); cmd != nil {
		t.Fatal("disabled /plugins returned a command")
	}
	view := model.View()
	if !strings.Contains(view, "Plugins are disabled.") || !strings.Contains(view, "Enable the plugins feature to use /plugins.") {
		t.Fatalf("disabled plugin history missing:\n%s", view)
	}
}

func pluginBrowserTestResponse(marketplacePath string, display string, short string, installed bool, enabled bool) plugin.PluginListResponse {
	return plugin.PluginListResponse{Marketplaces: []plugin.PluginMarketplaceEntry{{
		Name: "team",
		Path: &marketplacePath,
		Plugins: []plugin.PluginSummary{{
			ID:              "docs@team",
			Name:            "docs",
			MarketplaceName: "team",
			Availability:    plugin.PluginAvailable,
			InstallPolicy:   plugin.InstallAllowed,
			Installed:       installed,
			Enabled:         enabled,
			Interface: &plugin.PluginInterface{
				DisplayName:      &display,
				ShortDescription: &short,
			},
		}},
	}}}
}

func pluginBrowserTestDetail(response plugin.PluginListResponse) plugin.PluginDetail {
	marketplace := response.Marketplaces[0]
	short := "Search docs."
	return plugin.PluginDetail{
		MarketplaceName: marketplace.Name,
		MarketplacePath: marketplace.Path,
		Summary:         marketplace.Plugins[0],
		Description:     &short,
	}
}

func selectPluginViewAction(t *testing.T, model *Model, action chatwidget.UsageMenuAction) {
	t.Helper()
	state := model.pluginBrowserState()
	if state == nil || state.view == nil {
		t.Fatalf("plugin selection view is not open for action %q", action)
	}
	for i, item := range state.view.Items {
		if item.Action == action {
			state.viewSelected = i
			return
		}
	}
	t.Fatalf("plugin action %q not found in %#v", action, state.view.Items)
}

func mustPluginCmd(t *testing.T, cmd bubbletea.Cmd, name string) bubbletea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatalf("%s returned no command", name)
	}
	return cmd
}
