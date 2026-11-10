package plugin

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"time"
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
// Rust compares, through the shared field-change list so invalidation and its
// diagnostics never drift (#60505).
func installedPluginMetadataEqual(previous PluginDetail, current PluginDetail) bool {
	return len(installedPluginMetadataFieldChanges(previous.Summary, current.Summary)) == 0
}

// installedPluginMetadataFieldChanges names the behavior-determining fields that
// differ, using Rust's remote_metadata.rs field names (#60505). Only names are
// reported so diagnostics never log plugin values. Go has no `canonical_app_id`
// field, so that Rust entry has no counterpart here.
func installedPluginMetadataFieldChanges(previous PluginSummary, current PluginSummary) []string {
	var changed []string
	add := func(field string, differs bool) {
		if differs {
			changed = append(changed, field)
		}
	}
	add("marketplace_name", previous.MarketplaceName != current.MarketplaceName)
	add("id", previous.RemotePluginID != current.RemotePluginID)
	add("name", previous.Name != current.Name)
	add("version", !reflect.DeepEqual(previous.Version, current.Version))
	add("installed_at", !reflect.DeepEqual(previous.InstalledAt, current.InstalledAt))
	add("enabled", previous.Enabled != current.Enabled)
	add("install_policy", previous.InstallPolicy != current.InstallPolicy)
	add("install_policy_source", !reflect.DeepEqual(previous.InstallPolicySource, current.InstallPolicySource))
	add("must_show_installation_interstitial", !reflect.DeepEqual(previous.MustShowInstallationInterstitial, current.MustShowInstallationInterstitial))
	add("auth_policy", previous.AuthPolicy != current.AuthPolicy)
	add("availability", previous.Availability != current.Availability)
	add("disabled_reason", !reflect.DeepEqual(previous.DisabledReason, current.DisabledReason))
	add("eligible_plan_types", !reflect.DeepEqual(previous.EligiblePlanTypes, current.EligiblePlanTypes))
	return changed
}

// InstalledPluginMetadataChanges attributes every behavior-determining metadata
// change to the remote plugin id it belongs to (Rust #60505
// `installed_plugin_metadata_changes`). Callers only use it for an already
// changed snapshot, so an unchanged plugin never reports an empty entry.
//
// Plugins are matched by remote id instead of position: an insertion or removal
// must not misattribute every following plugin's fields to its neighbour. A pure
// reordering of an otherwise identical membership reports `position`.
func InstalledPluginMetadataChanges(previous []PluginDetail, current []PluginDetail) map[string][]string {
	type priorPlugin struct {
		index  int
		detail PluginDetail
	}
	prior := make(map[string]priorPlugin, len(previous))
	for index, detail := range previous {
		prior[detail.Summary.RemotePluginID] = priorPlugin{index: index, detail: detail}
	}
	sameMembers := len(previous) == len(current)
	if sameMembers {
		for _, detail := range current {
			if _, ok := prior[detail.Summary.RemotePluginID]; !ok {
				sameMembers = false
				break
			}
		}
	}
	changes := make(map[string][]string)
	for index, detail := range current {
		id := detail.Summary.RemotePluginID
		previousDetail, ok := prior[id]
		if !ok {
			changes[id] = []string{"added"}
			continue
		}
		delete(prior, id)
		fields := installedPluginMetadataFieldChanges(previousDetail.detail.Summary, detail.Summary)
		if previousDetail.index != index && sameMembers {
			// A removed plugin shifts later indices without reordering survivors.
			fields = append(fields, "position")
		}
		if len(fields) > 0 {
			changes[id] = fields
		}
	}
	for id := range prior {
		changes[id] = []string{"removed"}
	}
	return changes
}

// NewInstalledPluginMetadataChangeID joins a metadata invalidation to the MCP
// refresh it triggers (Rust #60505 correlates the two through one change id).
func NewInstalledPluginMetadataChangeID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("metadata-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
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
