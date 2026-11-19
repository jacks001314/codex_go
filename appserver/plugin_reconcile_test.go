package appserver

import (
	"testing"

	"codex_go/plugin"
)

func TestPluginReconcileSignatureDetectsCapabilityChanges(t *testing.T) {
	base := plugin.PluginDetail{
		Summary:    plugin.PluginSummary{ID: "acme/weather", Enabled: true},
		MCPServers: []string{"weather"},
	}
	changed := base
	changed.Hooks = []plugin.PluginHookSummary{{Key: "post_tool_use"}}
	if pluginReconcileSignature(base) == pluginReconcileSignature(changed) {
		t.Fatal("pluginReconcileSignature should differ when hooks change")
	}
}

// Rust #62105: an advertised release, install policy, availability, or display
// tag change must keep the live runtime.
func TestPluginReconcileSignatureIgnoresCatalogMetadataLikeRust(t *testing.T) {
	version := "1.0.0"
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{
		ID: "acme/weather", Name: "weather", MarketplaceName: "acme", Enabled: true,
		Version: &version, InstallPolicy: plugin.InstallAllowed, Availability: plugin.PluginAvailable,
		AuthPolicy: plugin.AuthOnUse, PluginDisplayNameTag: "Weather",
	}}
	for _, tc := range []struct {
		name   string
		mutate func(*plugin.PluginDetail)
	}{
		{"version", func(detail *plugin.PluginDetail) {
			next := "2.0.0"
			detail.Summary.Version = &next
		}},
		{"local version", func(detail *plugin.PluginDetail) {
			next := "2.0.0"
			detail.Summary.LocalVersion = &next
		}},
		{"install policy", func(detail *plugin.PluginDetail) { detail.Summary.InstallPolicy = plugin.InstallBlocked }},
		{"availability", func(detail *plugin.PluginDetail) { detail.Summary.Availability = plugin.PluginDisabledByAdmin }},
		{"auth policy", func(detail *plugin.PluginDetail) { detail.Summary.AuthPolicy = plugin.AuthOnInstall }},
		{"display name tag", func(detail *plugin.PluginDetail) { detail.Summary.PluginDisplayNameTag = "Forecast" }},
		{"install suggestion", func(detail *plugin.PluginDetail) { detail.Summary.InstallSuggestion = true }},
		{"disabled reason", func(detail *plugin.PluginDetail) {
			detail.Summary.DisabledReason = pluginDisabledReasonPtr(plugin.PluginPlanNotEligibleReason)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := base
			tc.mutate(&next)
			if pluginReconcileSignature(base) != pluginReconcileSignature(next) {
				t.Fatalf("a catalog %s change must not invalidate the runtime", tc.name)
			}
		})
	}
}

func TestPluginReconcileSignatureDetectsActiveBundleChangesLikeRust(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Name: "weather", MarketplaceName: "acme", Enabled: true}}
	first := base
	first.ActiveBundle = &plugin.PluginActiveBundle{DirectoryVersion: "1.0.0"}
	second := base
	second.ActiveBundle = &plugin.PluginActiveBundle{DirectoryVersion: "2.0.0"}
	if pluginReconcileSignature(first) == pluginReconcileSignature(second) {
		t.Fatal("pluginReconcileSignature should differ when the active bundle changes")
	}
	remoteID := "plugin-weather"
	receipt := base
	receipt.ActiveBundle = &plugin.PluginActiveBundle{DirectoryVersion: "1.0.0", RemotePluginID: &remoteID}
	if pluginReconcileSignature(first) == pluginReconcileSignature(receipt) {
		t.Fatal("pluginReconcileSignature should differ when the persisted remote id changes")
	}
	disabled := second
	disabled.Summary.Enabled = false
	disabled.ActiveBundle = nil
	if pluginReconcileSignature(base) == pluginReconcileSignature(disabled) {
		t.Fatal("pluginReconcileSignature should differ when enablement changes")
	}
}

func TestPluginReconcileSignatureDetectsCanonicalOwnershipChangeLikeRust(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Name: "weather", MarketplaceName: "acme", Enabled: true}}
	next := base
	owner := "connector-weather"
	next.CanonicalAppID = &owner
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when canonical ownership changes")
	}
}

func TestPluginReconcileSignatureDetectsRemotePluginIDChange(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true, RemotePluginID: "remote-1"}}
	next := base
	next.Summary.RemotePluginID = "remote-2"
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when remote plugin IDs change")
	}
}

func TestPluginReconcileSignatureDetectsEnablementChange(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true}}
	next := base
	next.Summary.Enabled = false
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when enablement changes")
	}
}

func TestPluginReconcileSignatureDetectsSkillsChange(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true, HasSkills: false}}
	next := base
	next.Summary.HasSkills = true
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when skills presence changes")
	}
}

func TestPluginReconcileChangedCapabilitiesUnionReplacedBundleLikeRust(t *testing.T) {
	previous := plugin.PluginDetail{
		Summary:    plugin.PluginSummary{ID: "acme/weather"},
		MCPServers: []string{"weather"},
		Hooks:      []plugin.PluginHookSummary{{Key: "post_tool_use"}},
	}
	current := plugin.PluginDetail{
		Summary:      plugin.PluginSummary{ID: "acme/weather", HasSkills: true},
		Apps:         []plugin.AppSummary{{ID: "weather-app"}},
		AppTemplates: []plugin.AppTemplateSummary{{ID: "weather-template"}},
	}

	got := pluginReconcileChangedCapabilities("acme/weather", previous, true, current, true)
	if got.ID != "acme/weather" || !got.HasMCPS || !got.HasApps || !got.HasHooks || !got.HasSkills {
		t.Fatalf("changed capabilities = %#v, want the union of both bundles", got)
	}
}

func TestPluginReconcileChangedCapabilitiesKeepRemovedBundleLikeRust(t *testing.T) {
	previous := plugin.PluginDetail{
		Summary:    plugin.PluginSummary{ID: "acme/weather", HasSkills: true},
		MCPServers: []string{"weather"},
		Hooks:      []plugin.PluginHookSummary{{Key: "post_tool_use"}},
	}

	got := pluginReconcileChangedCapabilities("acme/weather", previous, true, plugin.PluginDetail{}, false)
	if !got.HasMCPS || got.HasApps || !got.HasHooks || !got.HasSkills {
		t.Fatalf("removed plugin capabilities = %#v, want declarations from the deleted bundle", got)
	}
}

func TestPluginReconcileChangedCapabilitiesUseNewBundleOnlyLikeRust(t *testing.T) {
	current := plugin.PluginDetail{
		Summary:      plugin.PluginSummary{ID: "acme/weather"},
		AppTemplates: []plugin.AppTemplateSummary{{ID: "weather-template"}},
	}

	got := pluginReconcileChangedCapabilities("acme/weather", plugin.PluginDetail{}, false, current, true)
	if got.HasMCPS || !got.HasApps || got.HasHooks || got.HasSkills {
		t.Fatalf("new plugin capabilities = %#v, want only the installed bundle", got)
	}
}

func pluginDisabledReasonPtr(value plugin.PluginDisabledReason) *plugin.PluginDisabledReason {
	return &value
}

func TestRemoteInstalledPluginSyncFailuresSnapshotAndClear(t *testing.T) {
	clearRemoteInstalledPluginSyncFailures()
	recordRemoteInstalledPluginMaterializationFailure("acme/weather")
	recordRemoteInstalledPluginMaterializationFailure("acme/weather")
	failedRemote, failedMaterialization := takeRemoteInstalledPluginSyncFailures()
	if len(failedRemote) != 0 {
		t.Fatalf("failedRemote = %#v", failedRemote)
	}
	if len(failedMaterialization) != 2 || failedMaterialization[0] != "acme/weather" {
		t.Fatalf("failedMaterialization = %#v", failedMaterialization)
	}
	failedRemote, failedMaterialization = takeRemoteInstalledPluginSyncFailures()
	if len(failedRemote) != 0 || len(failedMaterialization) != 0 {
		t.Fatalf("second take = %#v / %#v", failedRemote, failedMaterialization)
	}
}
