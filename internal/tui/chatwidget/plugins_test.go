package chatwidget

import (
	"strings"
	"testing"

	pluginapi "codex_go/internal/plugin"
)

func TestMarketplaceProductLabelsAndDisplayNamesMatchRust(t *testing.T) {
	personalPath := `C:\Users\me\.agents\plugins\marketplace.json`
	customLabel := "Team Plugins"
	cases := []struct {
		entry pluginapi.PluginMarketplaceEntry
		want  string
	}{
		{entry: pluginapi.PluginMarketplaceEntry{Name: OpenAICuratedMarketplaceName}, want: "OpenAI Curated"},
		{entry: pluginapi.PluginMarketplaceEntry{Name: RemoteGlobalMarketplaceName}, want: "OpenAI Curated"},
		{entry: pluginapi.PluginMarketplaceEntry{Name: RemoteWorkspaceMarketplace}, want: "Workspace"},
		{entry: pluginapi.PluginMarketplaceEntry{Name: RemoteWorkspaceSharedPrivate}, want: "Shared with me"},
		{entry: pluginapi.PluginMarketplaceEntry{Name: RemoteWorkspaceSharedUnlisted}, want: "Shared with me (link)"},
		{entry: pluginapi.PluginMarketplaceEntry{Name: "codex-curated", Path: &personalPath}, want: "Local"},
		{entry: pluginapi.PluginMarketplaceEntry{Name: "team", Interface: pluginapi.MarketplaceInterface{DisplayName: &customLabel}}, want: "Team Plugins"},
	}
	for _, tc := range cases {
		if got := MarketplaceDisplayName(tc.entry); got != tc.want {
			t.Fatalf("MarketplaceDisplayName(%q) = %q, want %q", tc.entry.Name, got, tc.want)
		}
	}
}

func TestMarketplaceTabIDMatchingSavedIDFallsBackToNestedPath(t *testing.T) {
	path := `C:\repo\.agents\plugins\marketplace.json`
	entry := pluginapi.PluginMarketplaceEntry{Name: "local", Path: &path}
	if got := MarketplaceTabID(entry); got != MarketplaceTabIDPrefix+path {
		t.Fatalf("MarketplaceTabID() = %q", got)
	}
	if got, ok := MarketplaceTabIDMatchingSavedID(MarketplaceTabIDPrefix+`C:\repo`, []pluginapi.PluginMarketplaceEntry{entry}); !ok || got != MarketplaceTabIDPrefix+path {
		t.Fatalf("MarketplaceTabIDMatchingSavedID() = %q/%v", got, ok)
	}
}

func TestPluginStatusLabelMatchesRust(t *testing.T) {
	cases := []struct {
		name   string
		plugin pluginapi.PluginSummary
		want   string
	}{
		{name: "disabled by admin", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginDisabledByAdmin}, want: "Disabled"},
		{name: "admin assigned", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginAvailable, InstallPolicy: pluginapi.InstallInstalledByDefault}, want: "Admin assigned"},
		{name: "installed enabled", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginAvailable, Installed: true, Enabled: true}, want: "Installed"},
		{name: "installed disabled", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginAvailable, Installed: true, Enabled: false}, want: "Disabled"},
		{name: "not installable", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginAvailable, InstallPolicy: pluginapi.InstallBlocked}, want: "Not installable"},
		{name: "available", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginAvailable, InstallPolicy: pluginapi.InstallAllowed}, want: "Available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PluginStatusLabel(tc.plugin); got != tc.want {
				t.Fatalf("PluginStatusLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPluginDetailStatusLabelMatchesRust(t *testing.T) {
	cases := []struct {
		name   string
		plugin pluginapi.PluginSummary
		want   string
	}{
		{name: "disabled by admin", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginDisabledByAdmin}, want: "Disabled by admin"},
		{name: "admin installed", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginAvailable, InstallPolicy: pluginapi.InstallInstalledByDefault, Installed: true}, want: "Installed by admin"},
		{name: "admin enabled", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginAvailable, InstallPolicy: pluginapi.InstallInstalledByDefault}, want: "Enabled by Admin"},
		{name: "installed enabled", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginAvailable, Installed: true, Enabled: true}, want: "Installed"},
		{name: "installed disabled", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginAvailable, Installed: true, Enabled: false}, want: "Disabled"},
		{name: "not installable", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginAvailable, InstallPolicy: pluginapi.InstallBlocked}, want: "Not installable"},
		{name: "available", plugin: pluginapi.PluginSummary{Availability: pluginapi.PluginAvailable, InstallPolicy: pluginapi.InstallAllowed}, want: "Can be installed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PluginDetailStatusLabel(tc.plugin); got != tc.want {
				t.Fatalf("PluginDetailStatusLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPluginDisplayDescriptionAndBriefDescription(t *testing.T) {
	display := "Docs"
	short := "Short docs."
	long := "Long docs."
	plugin := pluginapi.PluginSummary{
		Name:          "docs",
		Availability:  pluginapi.PluginAvailable,
		InstallPolicy: pluginapi.InstallAllowed,
		Interface:     &pluginapi.PluginInterface{DisplayName: &display, ShortDescription: &short, LongDescription: &long},
	}
	if got := PluginDisplayName(plugin); got != "Docs" {
		t.Fatalf("PluginDisplayName() = %q", got)
	}
	if got, ok := PluginDescription(plugin); !ok || got != short {
		t.Fatalf("PluginDescription() = %q/%v", got, ok)
	}
	if got := PluginBriefDescription(plugin, "Workspace", len("Available")); got != "Available · Workspace · Short docs." {
		t.Fatalf("PluginBriefDescription() = %q", got)
	}
}

func TestPluginCatalogPopupModelMatchesRustTabsAndSelection(t *testing.T) {
	localPathOne := `C:\one\.agents\plugins\marketplace.json`
	localPathTwo := `C:\two\.agents\plugins\marketplace.json`
	displayOne := "Docs"
	shortOne := "Search docs."
	response := pluginapi.PluginListResponse{Marketplaces: []pluginapi.PluginMarketplaceEntry{
		{Name: RemoteGlobalMarketplaceName},
		{
			Name: "local-one",
			Path: &localPathOne,
			Plugins: []pluginapi.PluginSummary{{
				ID:            "docs@local",
				Name:          "docs",
				Availability:  pluginapi.PluginAvailable,
				InstallPolicy: pluginapi.InstallAllowed,
				Installed:     true,
				Enabled:       true,
				Interface:     &pluginapi.PluginInterface{DisplayName: &displayOne, ShortDescription: &shortOne},
				Source:        pluginapi.PluginSource{Type: "local", Path: "docs"},
			}},
		},
		{
			Name: "local-two",
			Path: &localPathTwo,
			Plugins: []pluginapi.PluginSummary{{
				ID:            "blocked@local",
				Name:          "blocked",
				Availability:  pluginapi.PluginDisabledByAdmin,
				InstallPolicy: pluginapi.InstallAllowed,
				Installed:     true,
				Source:        pluginapi.PluginSource{Type: "local", Path: "blocked"},
			}},
		},
	}}

	model := NewPluginCatalogPopupModel(response, PluginCatalogPopupOptions{
		ActiveTabID:              MarketplaceTabIDPrefix + RemoteWorkspaceMarketplace,
		RemoteSectionsLoading:    true,
		VerticalSectionRequested: true,
		CanRemoveMarketplaces:    map[string]bool{"local-one": true},
		CanUpgradeMarketplaces:   map[string]bool{"local-one": true},
	})

	if model.ViewID != PluginsSelectionViewID || !model.Searchable || model.SearchPlaceholder != "Type to search plugins" {
		t.Fatalf("model chrome = %+v", model)
	}
	if model.InitialTabID != RemoteLoadingTabIDPrefix+"workspace-loading" {
		t.Fatalf("initial tab = %q", model.InitialTabID)
	}
	gotLabels := []string{}
	for _, tab := range model.Tabs {
		gotLabels = append(gotLabels, tab.Label)
	}
	for _, want := range []string{"All Plugins", "Installed (2)", "OpenAI Curated", "Workspace", "Shared with me", "Local (1/2)", "Local (2/2)", "Add Marketplace"} {
		if !containsString(gotLabels, want) {
			t.Fatalf("labels = %#v, missing %q", gotLabels, want)
		}
	}
	curated := pluginCatalogModelTab(model, OpenAICuratedTabID)
	if curated == nil || len(curated.Items) != 1 || curated.Items[0].Name != "Loading OpenAI Curated plugins..." || !curated.Items[0].Disabled {
		t.Fatalf("curated tab = %#v", curated)
	}
	localOne := pluginCatalogModelTab(model, MarketplaceTabIDFromPath(localPathOne))
	if localOne == nil || !strings.Contains(localOne.FooterHint, "ctrl + u upgrade") || !strings.Contains(localOne.FooterHint, "ctrl + r remove") {
		t.Fatalf("local one footer = %#v", localOne)
	}
	if len(localOne.Items) != 1 || localOne.Items[0].Toggle == nil || !localOne.Items[0].Toggle.IsOn || !strings.Contains(localOne.Items[0].SelectedDescription, "Space to disable; Enter view details.") {
		t.Fatalf("local one item = %#v", localOne.Items)
	}
	localTwo := pluginCatalogModelTab(model, MarketplaceTabIDFromPath(localPathTwo))
	if localTwo == nil || len(localTwo.Items) != 1 || localTwo.Items[0].TogglePlaceholder != "blocked" {
		t.Fatalf("local two item = %#v", localTwo)
	}
}

func TestPluginDetailViewMetadataAndPrimaryActionsMatchRust(t *testing.T) {
	path := `C:\repo\.agents\plugins\marketplace.json`
	long := "Long details."
	version := "1.0.0"
	display := "Docs"
	detail := pluginapi.PluginDetail{
		MarketplaceName: "local",
		MarketplacePath: &path,
		Summary: pluginapi.PluginSummary{
			ID:            "docs@local",
			Name:          "docs",
			Availability:  pluginapi.PluginAvailable,
			InstallPolicy: pluginapi.InstallAllowed,
			LocalVersion:  &version,
			Interface:     &pluginapi.PluginInterface{DisplayName: &display, LongDescription: &long},
			Source:        pluginapi.PluginSource{Type: "local", Path: "docs"},
			AuthPolicy:    pluginapi.AuthOnUse,
		},
		Skills:     []pluginapi.PluginSkill{{Name: "writer"}},
		Hooks:      []pluginapi.PluginHookSummary{{EventName: "Stop"}},
		Apps:       []pluginapi.AppSummary{{Name: "Drive"}},
		MCPServers: []string{"docs"},
	}

	view := NewPluginDetailView(detail)
	header := strings.Join(view.HeaderLines, "\n")
	if !strings.Contains(header, "Docs"+pluginSummarySeparator+"Can be installed"+pluginSummarySeparator+"Local") ||
		!strings.Contains(header, PluginCatalogAppsHelpURL) ||
		!strings.Contains(header, long) {
		t.Fatalf("header = %#v", view.HeaderLines)
	}
	if len(view.Items) < 8 || view.Items[1].Name != "Install plugin" || view.Items[1].Action != PluginMenuActionInstall || view.Items[1].Disabled {
		t.Fatalf("primary items = %#v", view.Items)
	}
	wantItems := map[string]string{
		"Source":      "Local",
		"Auth":        "Auth on use",
		"Version":     "local 1.0.0",
		"Skills":      "writer",
		"Hooks":       "Stop (1)",
		"Apps":        "Drive",
		"MCP Servers": "docs",
	}
	for name, want := range wantItems {
		item, ok := selectionItemByName(view.Items, name)
		if !ok || !strings.Contains(item.Description, want) || !item.Disabled {
			t.Fatalf("item %q = %#v ok=%v", name, item, ok)
		}
	}

	detail.Summary.Installed = true
	view = NewPluginDetailView(detail)
	if view.Items[1].Name != "Uninstall plugin" || view.Items[1].Action != PluginMenuActionUninstall || view.Items[1].ID != "docs@local" {
		t.Fatalf("uninstall action = %#v", view.Items[1])
	}

	detail.Summary.InstallPolicy = pluginapi.InstallInstalledByDefault
	view = NewPluginDetailView(detail)
	if view.Items[1].Name != "Installed by admin" || !view.Items[1].Disabled {
		t.Fatalf("admin item = %#v", view.Items[1])
	}
}

func TestPluginErrorAndMarketplaceViewsMatchRust(t *testing.T) {
	loadErr := PluginsErrorPopupView("offline")
	if loadErr.Subtitle != "Failed to load plugins." || len(loadErr.Items) != 1 || loadErr.Items[0].Name != "Plugin marketplace unavailable" || !loadErr.Items[0].Disabled {
		t.Fatalf("plugins error = %#v", loadErr)
	}

	addErr := MarketplaceAddErrorPopupView(true)
	if addErr.Subtitle != "Failed to add marketplace." || len(addErr.Items) != 3 ||
		addErr.Items[1].Name != "Try again" || addErr.Items[1].Action != PluginMenuActionAddMarketplace ||
		addErr.Items[2].Name != "Back to plugins" {
		t.Fatalf("add error = %#v", addErr)
	}

	confirm := MarketplaceRemoveConfirmationView("team", "Team")
	if len(confirm.HeaderLines) != 2 || !strings.Contains(confirm.HeaderLines[0], "Remove Team marketplace?") ||
		confirm.Items[0].Action != PluginMenuActionRemoveMarketplace || confirm.Items[1].Action != PluginMenuActionBackToPlugins {
		t.Fatalf("remove confirmation = %#v", confirm)
	}

	removeErr := MarketplaceRemoveErrorPopupView("team", "Team", true)
	if removeErr.Subtitle != "Failed to remove marketplace." || removeErr.Items[1].Action != PluginMenuActionRemoveMarketplace || removeErr.Items[1].ID != "team" {
		t.Fatalf("remove error = %#v", removeErr)
	}

	detailErr := PluginDetailErrorPopupView("missing", true)
	if detailErr.Subtitle != "Failed to load plugin details." || detailErr.Items[0].Name != "Plugin detail unavailable" || detailErr.Items[1].Action != PluginMenuActionBackToPlugins {
		t.Fatalf("detail error = %#v", detailErr)
	}
}

func TestPluginsCatalogViewUsesRuntimeCatalog(t *testing.T) {
	display := "Docs"
	short := "Search docs."
	version := "1.0.0"
	path := `D:\marketplaces\team`
	response := pluginapi.PluginListResponse{
		Marketplaces: []pluginapi.PluginMarketplaceEntry{{
			Name: "team",
			Path: &path,
			Plugins: []pluginapi.PluginSummary{{
				ID:            "docs@team",
				Name:          "docs",
				Availability:  pluginapi.PluginAvailable,
				InstallPolicy: pluginapi.InstallAllowed,
				Installed:     true,
				Enabled:       true,
				LocalVersion:  &version,
				Interface: &pluginapi.PluginInterface{
					DisplayName:      &display,
					ShortDescription: &short,
				},
				Keywords: []string{"search", "knowledge"},
			}},
		}},
	}

	view := NewPluginsCatalogView(response, MarketplaceTabIDFromPath(`D:\marketplaces`))
	if view.ViewID != PluginsSelectionViewID || view.Title != "Plugins" || !view.Searchable {
		t.Fatalf("view = %+v", view)
	}
	header := strings.Join(view.HeaderLines, "\n")
	if !strings.Contains(header, "Installed 1 of 1 available plugins.") || !strings.Contains(header, "Selected tab: "+MarketplaceTabIDFromPath(path)) {
		t.Fatalf("header = %q", header)
	}
	if len(view.Items) != 1 || view.Items[0].ID != "docs@team" || view.Items[0].Name != "Docs" {
		t.Fatalf("items = %+v", view.Items)
	}
	for _, want := range []string{"Installed", "team", "Search docs.", "local 1.0.0"} {
		if !strings.Contains(view.Items[0].Description, want) {
			t.Fatalf("description missing %q: %q", want, view.Items[0].Description)
		}
	}
	if !strings.Contains(view.Items[0].SearchValue, "knowledge") {
		t.Fatalf("search value = %q", view.Items[0].SearchValue)
	}
}

func TestPluginsCatalogViewEmptyAndErrors(t *testing.T) {
	view := NewPluginsCatalogView(pluginapi.PluginListResponse{
		MarketplaceLoadErrors: []pluginapi.MarketplaceLoadErrorInfo{{MarketplacePath: `D:\bad`, Message: "bad json"}},
	}, "")
	if len(view.Items) != 1 || !view.Items[0].Disabled || view.Items[0].Name != "No plugins found" {
		t.Fatalf("empty items = %+v", view.Items)
	}
	if !strings.Contains(strings.Join(view.HeaderLines, "\n"), `Failed to load D:\bad: bad json`) {
		t.Fatalf("header = %+v", view.HeaderLines)
	}
}

func TestPluginSourceAuthVersionAndShareSummaries(t *testing.T) {
	ref := "main"
	version := "1.2.3"
	remoteVersion := "2.0.0"
	discoverability := string(pluginapi.PluginShareDiscoverabilityUnlisted)
	shareURL := "https://example.test/share"
	creatorName := "Ana"
	creatorID := "user-1"
	detail := pluginapi.PluginDetail{
		MarketplaceName: RemoteWorkspaceMarketplace,
		Summary: pluginapi.PluginSummary{
			Source:       pluginapi.PluginSource{Type: "git", URL: "https://example.test/repo.git", RefName: &ref},
			AuthPolicy:   pluginapi.AuthOnInstall,
			LocalVersion: &version,
			ShareContext: &pluginapi.PluginShareContext{
				RemotePluginID:       "plugins~Plugin_docs",
				RemoteVersion:        &remoteVersion,
				Discoverability:      &discoverability,
				ShareURL:             &shareURL,
				CreatorName:          &creatorName,
				CreatorAccountUserID: &creatorID,
				SharePrincipals:      []pluginapi.PluginSharePrincipal{{Name: "Workspace"}},
			},
		},
	}
	if got := PluginSourceSummary(detail); got != "Git · https://example.test/repo.git@main" {
		t.Fatalf("PluginSourceSummary() = %q", got)
	}
	if got := PluginAuthPolicySummary(detail.Summary.AuthPolicy); got != "Auth on install" {
		t.Fatalf("PluginAuthPolicySummary() = %q", got)
	}
	if got, ok := PluginVersionSummary(detail.Summary); !ok || got != "local 1.2.3 · remote 2.0.0" {
		t.Fatalf("PluginVersionSummary() = %q/%v", got, ok)
	}
	share := PluginShareContextSummary(detail.Summary.ShareContext)
	for _, want := range []string{"Workspace link", "creator Ana (user-1)", "1 principal: Workspace", shareURL} {
		if !strings.Contains(share, want) {
			t.Fatalf("PluginShareContextSummary() = %q, missing %q", share, want)
		}
	}
}

func TestPluginDetailCapabilitySummaries(t *testing.T) {
	detail := pluginapi.PluginDetail{
		Skills:     []pluginapi.PluginSkill{{Name: "writer"}, {Name: "reviewer"}},
		Apps:       []pluginapi.AppSummary{{Name: "Drive"}, {Name: "Calendar"}},
		Hooks:      []pluginapi.PluginHookSummary{{EventName: "PreToolUse"}, {EventName: "PreToolUse"}, {EventName: "Stop"}},
		MCPServers: []string{"docs", "issues"},
	}
	if got := PluginSkillSummary(detail); got != "writer, reviewer" {
		t.Fatalf("PluginSkillSummary() = %q", got)
	}
	if got := PluginAppSummary(detail); got != "Drive, Calendar" {
		t.Fatalf("PluginAppSummary() = %q", got)
	}
	if got := PluginHookSummary(detail); got != "PreToolUse (2), Stop (1)" {
		t.Fatalf("PluginHookSummary() = %q", got)
	}
	if got := PluginMCPSummary(detail); got != "docs, issues" {
		t.Fatalf("PluginMCPSummary() = %q", got)
	}
}

func TestPluginInstallAuthPopupView(t *testing.T) {
	url := "https://chatgpt.com/apps/docs"
	view, ok := PluginInstallAuthPopupView(
		PluginInstallAuthFlowState{PluginDisplayName: "Docs", NextAppIndex: 0},
		[]pluginapi.AppSummary{{ID: "docs", Name: "Docs", InstallURL: &url}},
		map[string]bool{"docs": true},
	)
	if !ok {
		t.Fatal("PluginInstallAuthPopupView() ok = false")
	}
	if view.ViewID != PluginsSelectionViewID || view.Subtitle != "Docs plugin installed." {
		t.Fatalf("view = %+v", view)
	}
	if view.Items[1].Name != "Manage on ChatGPT" || view.Items[2].Name != "Continue" || view.Items[3].Action != PluginMenuActionAuthFlowAbandon {
		t.Fatalf("items = %+v", view.Items)
	}
}

func TestMergeRemoteMarketplacesRemovesLocalCuratedAndStaleRemoteSections(t *testing.T) {
	localPath := `C:\marketplaces\openai-curated`
	response := &pluginapi.PluginListResponse{Marketplaces: []pluginapi.PluginMarketplaceEntry{
		{Name: OpenAICuratedMarketplaceName, Path: &localPath},
		{Name: RemoteWorkspaceMarketplace},
		{Name: "other"},
	}}
	MergeRemoteMarketplaces(response, []pluginapi.PluginMarketplaceEntry{{Name: RemoteGlobalMarketplaceName}, {Name: RemoteWorkspaceMarketplace}})
	got := []string{}
	for _, marketplace := range response.Marketplaces {
		got = append(got, marketplace.Name)
	}
	if strings.Join(got, ",") != "other,openai-curated-remote,workspace-directory" {
		t.Fatalf("marketplaces = %v", got)
	}
}

func pluginCatalogModelTab(model PluginCatalogPopupModel, tabID string) *PluginCatalogTabModel {
	for i := range model.Tabs {
		if model.Tabs[i].ID == tabID {
			return &model.Tabs[i]
		}
	}
	return nil
}

func selectionItemByName(items []SelectionItem, name string) (SelectionItem, bool) {
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return SelectionItem{}, false
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
