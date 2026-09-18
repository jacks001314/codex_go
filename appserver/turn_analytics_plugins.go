package appserver

import (
	"sort"

	"codex_go/plugin"
)

// Bounds from Rust's TurnContext::active_plugin_ids_for_telemetry.
const (
	maxTurnAnalyticsPluginIDs     = 512
	maxTurnAnalyticsPluginIDBytes = 128
)

// analyticsPluginIdentity mirrors utils_plugins::PluginIdentity: a local plugin
// key plus an optional remote identity.
type analyticsPluginIdentity struct {
	pluginID       string
	remotePluginID *string
}

// turnAnalyticsPluginInventory returns the plugin inventory observed for this
// turn. Rust #46323 samples the turn's identities once, from the context
// captured before pre-turn compaction, and keeps that first inventory, so a
// later rebuild (the post-compaction run-config reload) does not re-sample it.
// The first capture is memoized on the active turn.
func (r *RuntimeRouter) turnAnalyticsPluginInventory(threadID string, turnID string) *[]string {
	if r == nil {
		return nil
	}
	if r.threads == nil {
		return r.activePluginIDsForTurnAnalytics(threadID)
	}
	captured := false
	r.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) {
		captured = active.PluginInventoryCaptured
	})
	if captured {
		var inventory *[]string
		r.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) {
			inventory = active.PluginInventory
		})
		return inventory
	}
	inventory := r.activePluginIDsForTurnAnalytics(threadID)
	r.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) {
		if !active.PluginInventoryCaptured {
			active.PluginInventoryCaptured = true
			active.PluginInventory = inventory
		}
	})
	return inventory
}

// activePluginIDsForTurnAnalytics mirrors Rust's
// TurnContext::active_plugin_ids_for_telemetry (#46323): the turn's active host
// plugin identities combined with the plugin package keys selected for the
// thread, preferring a valid remote identity over the package key, sorted and
// deduplicated.
//
// It returns nil - Rust's `None`, serialized as JSON null - when the inventory
// is unknown: no plugin service observes the thread, any identity is invalid,
// an id exceeds 128 bytes, or more than 512 distinct ids remain. An observed
// empty inventory is returned as a non-nil empty slice (JSON []).
func (r *RuntimeRouter) activePluginIDsForTurnAnalytics(threadID string) *[]string {
	if r == nil || r.services.Plugins == nil {
		return nil
	}
	identities := make([]analyticsPluginIdentity, 0, 8)
	for _, capability := range r.pluginCapabilitiesForThread(threadID) {
		identity := analyticsPluginIdentity{pluginID: capability.ConfigName}
		if capability.RemotePluginID != "" {
			// The remote id is used verbatim: Rust validates it without trimming.
			remote := capability.RemotePluginID
			identity.remotePluginID = &remote
		}
		identities = append(identities, identity)
	}
	// Selected capability roots provide a package key, not a remote identity.
	selected := r.executorSkillRootPluginIDs(threadID)
	for _, pluginID := range selected {
		identities = append(identities, analyticsPluginIdentity{pluginID: pluginID})
	}
	return activePluginTelemetryIDs(identities)
}

// activePluginTelemetryIDs mirrors Rust's identity selection and the
// complete-inventory bounds: a valid remote identity wins over the package key,
// otherwise the package key must parse; any invalid identity, an id longer than
// 128 bytes, or more than 512 distinct ids makes the whole inventory unknown.
func activePluginTelemetryIDs(identities []analyticsPluginIdentity) *[]string {
	ids := make([]string, 0, len(identities))
	for _, identity := range identities {
		id := identity.pluginID
		if identity.remotePluginID != nil {
			if !plugin.IsValidRemotePluginID(*identity.remotePluginID) {
				return nil
			}
			id = *identity.remotePluginID
		} else if _, err := plugin.ParsePluginId(identity.pluginID); err != nil {
			return nil
		}
		if len(id) > maxTurnAnalyticsPluginIDBytes {
			return nil
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	unique := make([]string, 0, len(ids))
	for index, id := range ids {
		if index > 0 && id == ids[index-1] {
			continue
		}
		unique = append(unique, id)
	}
	if len(unique) > maxTurnAnalyticsPluginIDs {
		return nil
	}
	return &unique
}
