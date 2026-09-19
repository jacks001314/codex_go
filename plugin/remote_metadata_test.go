package plugin

import "testing"

const remoteMetadataMarketplace = "openai-curated-remote"

func remoteMetadataImageURL(renewal string) *string {
	value := "https://files.openai.com/plugins/icon.png?sig=" + renewal
	return &value
}

// remoteMetadataPlugin builds one installed remote plugin, mirroring Rust
// remote_metadata_tests.rs::plugin. Only the interface imagery is
// parameterized: it is display metadata, so renewing it must not invalidate.
func remoteMetadataPlugin(renewal string) PluginDetail {
	version := "1.0.0"
	return PluginDetail{
		MarketplaceName: remoteMetadataMarketplace,
		Summary: PluginSummary{
			ID:              pluginID("test", remoteMetadataMarketplace),
			Name:            "test",
			DisplayName:     "Test plugin",
			Description:     "installed remote plugin",
			MarketplaceName: remoteMetadataMarketplace,
			RemotePluginID:  "plugin-test",
			Version:         &version,
			Installed:       true,
			Enabled:         true,
			InstallPolicy:   InstallAllowed,
			AuthPolicy:      AuthOnUse,
			Availability:    PluginAvailable,
			Keywords:        []string{},
			Interface: &PluginInterface{
				LogoURL:         remoteMetadataImageURL(renewal),
				LogoURLDark:     remoteMetadataImageURL(renewal),
				ComposerIconURL: remoteMetadataImageURL(renewal),
				ScreenshotURLs:  []string{*remoteMetadataImageURL(renewal)},
				Capabilities:    []string{"badge"},
			},
		},
	}
}

// Mirrors Rust remote_metadata_tests.rs::behavioral_metadata_changes_still_invalidate.
func TestInstalledPluginMetadataBehavioralChangesStillInvalidateLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*PluginDetail)
	}{
		{"remote plugin id", func(detail *PluginDetail) { detail.Summary.RemotePluginID += "-other" }},
		{"name", func(detail *PluginDetail) { detail.Summary.Name += "-other" }},
		{"version", func(detail *PluginDetail) { value := "2.0.0"; detail.Summary.Version = &value }},
		{"installed at", func(detail *PluginDetail) { value := int64(1); detail.Summary.InstalledAt = &value }},
		{"enabled", func(detail *PluginDetail) { detail.Summary.Enabled = false }},
		{"install policy", func(detail *PluginDetail) { detail.Summary.InstallPolicy = InstallBlocked }},
		{"install policy source", func(detail *PluginDetail) {
			source := PluginInstallPolicySourceWorkspaceSetting
			detail.Summary.InstallPolicySource = &source
		}},
		{"interstitial", func(detail *PluginDetail) { shown := true; detail.Summary.MustShowInstallationInterstitial = &shown }},
		{"auth policy", func(detail *PluginDetail) { detail.Summary.AuthPolicy = AuthOnInstall }},
		{"availability", func(detail *PluginDetail) { detail.Summary.Availability = PluginDisabledByAdmin }},
		{"disabled reason", func(detail *PluginDetail) {
			reason := PluginPlanNotEligibleReason
			detail.Summary.DisabledReason = &reason
		}},
		{"eligible plan types", func(detail *PluginDetail) {
			plans := []string{"enterprise"}
			detail.Summary.EligiblePlanTypes = &plans
		}},
		{"marketplace name", func(detail *PluginDetail) {
			detail.Summary.MarketplaceName = "created-by-me-remote"
			detail.MarketplaceName = "created-by-me-remote"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := []PluginDetail{remoteMetadataPlugin("old")}
			current := []PluginDetail{remoteMetadataPlugin("new")}
			tc.mutate(&current[0])
			if InstalledPluginMetadataEqual(previous, current) {
				t.Fatalf("%s change must invalidate the loaded plugins", tc.name)
			}
		})
	}
	if InstalledPluginMetadataEqual([]PluginDetail{remoteMetadataPlugin("old")}, nil) {
		t.Fatal("removing every installed plugin must invalidate")
	}
}

// Mirrors Rust remote_metadata_tests.rs::display_metadata_does_not_invalidate and
// ::store_badge_changes_do_not_invalidate: renewed imagery, changed badges,
// descriptions, keywords, display names, and other derived/display fields do not
// invalidate.
func TestInstalledPluginMetadataDisplayChangesDoNotInvalidateLikeRust(t *testing.T) {
	previous := []PluginDetail{remoteMetadataPlugin("old")}
	current := []PluginDetail{remoteMetadataPlugin("new")}
	summary := &current[0].Summary
	summary.DisplayName = "Renamed plugin"
	summary.Description = "a fresh description"
	summary.Keywords = []string{"new", "keywords"}
	summary.Interface.DisplayName = stringPointerForPluginTest("New display name")
	summary.Interface.ShortDescription = stringPointerForPluginTest("New description")
	summary.Interface.BrandColor = stringPointerForPluginTest("#123456")
	summary.Interface.WebsiteURL = stringPointerForPluginTest("https://another-host.test")
	summary.Interface.DefaultPrompt = []string{"New starter prompt"}
	summary.Interface.Capabilities = []string{"skills", "tools"}
	summary.Interface.ScreenshotURLs = nil
	summary.LocalVersion = stringPointerForPluginTest("0.9.0")
	summary.HasSkills = false
	summary.MCPServers = []string{"demo"}
	summary.AppConnectors = []string{"connector"}
	current[0].Skills = []PluginSkill{{Name: "deploy"}}
	current[0].Hooks = []PluginHookSummary{{Key: "post_tool_use"}}
	current[0].Apps = []AppSummary{{ID: "demo"}}
	current[0].AppTemplates = []AppTemplateSummary{{ID: "template"}}
	current[0].MCPServers = []string{"demo"}
	if !InstalledPluginMetadataEqual(previous, current) {
		t.Fatal("display-only metadata changes must keep the loaded plugins")
	}
	// Rust also pins that dropping the interface entirely is display-only.
	current[0].Summary.Interface = nil
	if !InstalledPluginMetadataEqual(previous, current) {
		t.Fatal("removing the interface must not invalidate")
	}
}

// Mirrors Rust remote_metadata_tests.rs::installed_ordering_remains_significant.
func TestInstalledPluginMetadataOrderingRemainsSignificantLikeRust(t *testing.T) {
	other := remoteMetadataPlugin("old")
	other.Summary.RemotePluginID += "-other"
	other.Summary.Name += "-other"
	previous := []PluginDetail{remoteMetadataPlugin("old"), other}
	current := []PluginDetail{previous[1], previous[0]}
	if InstalledPluginMetadataEqual(previous, current) {
		t.Fatal("installed plugin order must remain significant")
	}
}

// Mirrors Rust #46309 at the service boundary: a display-only refresh keeps the
// behavior snapshot but still publishes the fresh payload, while a behavioral
// change reports a refresh.
func TestReplaceInstalledRemotePluginsOnlyReportsBehavioralChangesLikeRust(t *testing.T) {
	service := NewPluginService()
	if changed := service.ReplaceInstalledRemotePlugins(remoteMetadataMarketplace, []PluginDetail{remoteMetadataPlugin("first")}); !changed {
		t.Fatal("the first publication must report a change")
	}
	if changed := service.ReplaceInstalledRemotePlugins(remoteMetadataMarketplace, []PluginDetail{remoteMetadataPlugin("second")}); changed {
		t.Fatal("a renewed image URL must not report a change")
	}
	// The unmodified payload is still published for display consumers.
	stored := installedDetailByRemoteID(t, service, "plugin-test")
	if stored.Summary.Interface == nil || stored.Summary.Interface.LogoURL == nil || *stored.Summary.Interface.LogoURL != *remoteMetadataImageURL("second") {
		t.Fatalf("renewed interface was not published: %#v", stored.Summary.Interface)
	}

	renamed := remoteMetadataPlugin("second")
	renamed.Summary.Version = stringPointerForPluginTest("2.0.0")
	if changed := service.ReplaceInstalledRemotePlugins(remoteMetadataMarketplace, []PluginDetail{renamed}); !changed {
		t.Fatal("a version change must report a change")
	}
	if changed := service.ReplaceInstalledRemotePlugins(remoteMetadataMarketplace, nil); !changed {
		t.Fatal("removing every installed plugin must report a change")
	}
}

func installedDetailByRemoteID(t *testing.T, service *PluginService, remotePluginID string) PluginDetail {
	t.Helper()
	for _, detail := range service.InstalledDetails() {
		if detail.Summary.RemotePluginID == remotePluginID {
			return detail
		}
	}
	t.Fatalf("installed plugin %q was not found", remotePluginID)
	return PluginDetail{}
}

// Mirrors Rust remote_metadata_tests.rs::catalog_display_reordering_does_not_invalidate:
// catalog comparison ignores plugin display order but keeps marketplace order and
// membership significant.
func TestRemoteCatalogMetadataReorderingAndBehaviorLikeRust(t *testing.T) {
	first := remoteMetadataPlugin("old")
	first.Summary.Interface.DisplayName = stringPointerForPluginTest("Alpha")
	second := remoteMetadataPlugin("old")
	second.Summary.ID = pluginID("test-second", remoteMetadataMarketplace)
	second.Summary.Name = "test-second"
	second.Summary.RemotePluginID = "plugin-test-second"
	second.Summary.Interface.DisplayName = stringPointerForPluginTest("Beta")
	previousCatalog := []PluginMarketplaceEntry{{
		Name:    remoteMetadataMarketplace,
		Plugins: []PluginSummary{first.Summary, second.Summary},
	}}

	// Display-name reordering only changes the display order of the plugins.
	reordered := []PluginMarketplaceEntry{{
		Name:    remoteMetadataMarketplace,
		Plugins: []PluginSummary{second.Summary, first.Summary},
	}}
	if !RemoteCatalogMetadataEqual(previousCatalog, reordered) {
		t.Fatal("plugin display reordering must not invalidate the catalog")
	}

	changedVersion := remoteMetadataPlugin("old")
	changedVersion.Summary.Version = stringPointerForPluginTest("2.0.0")
	changedCatalog := []PluginMarketplaceEntry{{
		Name:    remoteMetadataMarketplace,
		Plugins: []PluginSummary{changedVersion.Summary, second.Summary},
	}}
	if RemoteCatalogMetadataEqual(previousCatalog, changedCatalog) {
		t.Fatal("a version change must invalidate the catalog")
	}
	if RemoteCatalogMetadataEqual(previousCatalog, []PluginMarketplaceEntry{{Name: remoteMetadataMarketplace, Plugins: []PluginSummary{first.Summary}}}) {
		t.Fatal("removing a plugin must invalidate the catalog")
	}
	duplicated := []PluginMarketplaceEntry{{
		Name:    remoteMetadataMarketplace,
		Plugins: []PluginSummary{second.Summary, second.Summary},
	}}
	if RemoteCatalogMetadataEqual(previousCatalog, duplicated) {
		t.Fatal("substituting a duplicate plugin must invalidate the catalog")
	}
	otherMarketplace := []PluginMarketplaceEntry{{
		Name:    "created-by-me-remote",
		Plugins: []PluginSummary{first.Summary, second.Summary},
	}}
	if RemoteCatalogMetadataEqual(previousCatalog, otherMarketplace) {
		t.Fatal("a marketplace rename must invalidate the catalog")
	}
}

func stringPointerForPluginTest(value string) *string { return &value }
