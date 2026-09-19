package plugin

import (
	"reflect"
	"sort"
)

// Remote metadata comparison (Rust core-plugins remote_metadata.rs, #46309).
//
// Only the metadata that determines installed-plugin behavior is compared.
// Display metadata (display name, description, interface imagery, capability
// badges, keywords) is excluded, so a display-only refresh - a renewed image URL
// or an updated badge - does not invalidate loaded plugins, live MCP sessions,
// or cached skill resources. Callers must still publish the unmodified payload
// so display consumers receive fresh metadata.

// InstalledPluginMetadataEqual reports whether two installed remote-plugin
// snapshots describe the same behavior, mirroring Rust's
// `installed_plugin_metadata_eq`: marketplace name, identity, version,
// installation time, enablement, installation/authentication policy,
// availability, and eligibility. Order remains significant, matching the Rust
// comparison (callers sort the map-backed Go snapshot into the published order).
func InstalledPluginMetadataEqual(previous []PluginDetail, current []PluginDetail) bool {
	if len(previous) != len(current) {
		return false
	}
	for index := range previous {
		if !installedPluginMetadataEqual(previous[index], current[index]) {
			return false
		}
	}
	return true
}

// installedPluginMetadataEqual compares exactly the behavior-determining fields
// Rust compares. Go has no `canonical_app_id` field, so the only Rust field
// without a Go counterpart is absent from both sides and never causes a
// mismatch.
func installedPluginMetadataEqual(previous PluginDetail, current PluginDetail) bool {
	a := previous.Summary
	b := current.Summary
	return a.MarketplaceName == b.MarketplaceName &&
		a.RemotePluginID == b.RemotePluginID &&
		a.Name == b.Name &&
		reflect.DeepEqual(a.Version, b.Version) &&
		reflect.DeepEqual(a.InstalledAt, b.InstalledAt) &&
		a.Enabled == b.Enabled &&
		a.InstallPolicy == b.InstallPolicy &&
		reflect.DeepEqual(a.InstallPolicySource, b.InstallPolicySource) &&
		reflect.DeepEqual(a.MustShowInstallationInterstitial, b.MustShowInstallationInterstitial) &&
		a.AuthPolicy == b.AuthPolicy &&
		a.Availability == b.Availability &&
		reflect.DeepEqual(a.DisabledReason, b.DisabledReason) &&
		reflect.DeepEqual(a.EligiblePlanTypes, b.EligiblePlanTypes)
}

// RemoteCatalogMetadataEqual reports whether two remote marketplace catalogs
// describe the same installation behavior, mirroring Rust's exported
// `remote_catalog_metadata_eq`. Marketplace order and plugin membership remain
// significant, while the plugin display order inside a marketplace is ignored
// (Rust sorts the view by remote plugin id first). Display metadata is excluded
// for the same reason as InstalledPluginMetadataEqual.
func RemoteCatalogMetadataEqual(previous []PluginMarketplaceEntry, current []PluginMarketplaceEntry) bool {
	if len(previous) != len(current) {
		return false
	}
	for index := range previous {
		if !remoteCatalogMarketplaceMetadataEqual(previous[index], current[index]) {
			return false
		}
	}
	return true
}

func remoteCatalogMarketplaceMetadataEqual(previous PluginMarketplaceEntry, current PluginMarketplaceEntry) bool {
	if previous.Name != current.Name || len(previous.Plugins) != len(current.Plugins) {
		return false
	}
	previousPlugins := append([]PluginSummary(nil), previous.Plugins...)
	currentPlugins := append([]PluginSummary(nil), current.Plugins...)
	sortPluginSummariesByRemotePluginID(previousPlugins)
	sortPluginSummariesByRemotePluginID(currentPlugins)
	for index := range previousPlugins {
		if !remoteCatalogPluginMetadataEqual(previousPlugins[index], currentPlugins[index]) {
			return false
		}
	}
	return true
}

func remoteCatalogPluginMetadataEqual(previous PluginSummary, current PluginSummary) bool {
	return previous.ID == current.ID &&
		previous.RemotePluginID == current.RemotePluginID &&
		previous.Name == current.Name &&
		reflect.DeepEqual(previous.Version, current.Version) &&
		reflect.DeepEqual(previous.LocalVersion, current.LocalVersion) &&
		previous.Installed == current.Installed &&
		reflect.DeepEqual(previous.InstalledAt, current.InstalledAt) &&
		previous.Enabled == current.Enabled &&
		previous.InstallPolicy == current.InstallPolicy &&
		reflect.DeepEqual(previous.InstallPolicySource, current.InstallPolicySource) &&
		reflect.DeepEqual(previous.MustShowInstallationInterstitial, current.MustShowInstallationInterstitial) &&
		previous.AuthPolicy == current.AuthPolicy &&
		previous.Availability == current.Availability &&
		reflect.DeepEqual(previous.DisabledReason, current.DisabledReason) &&
		reflect.DeepEqual(previous.EligiblePlanTypes, current.EligiblePlanTypes)
}

func sortPluginSummariesByRemotePluginID(summaries []PluginSummary) {
	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].RemotePluginID < summaries[j].RemotePluginID
	})
}
