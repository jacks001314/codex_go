package plugin

import "strings"

// This file mirrors the configurable default plugin activation policy added by
// upstream #52220 (Rust codex-rs/config/src/types.rs:1037-1084). The `plugins`
// config is a table whose reserved `_default` key carries the activation
// default for installed plugins, while every other key is an exact
// `<plugin>@<marketplace>` id with an optional explicit `enabled` override.
//
// The rule the Go side must reproduce is `PluginsConfigToml::allows_plugin`
// (types.rs:1059-1072): no `_default` preserves legacy source and account
// enablement, otherwise an explicit per-plugin setting overrides the configured
// default. The loader combines it with source enablement as
// `enabled.unwrap_or(true) && plugin_policy.allows_plugin(plugin_id)`
// (core-plugins/src/loader.rs:184-187), so configuration can narrow an
// installation but never activate one disabled at its source.

// PluginsDefaultConfig mirrors Rust `PluginsDefaultConfig` (types.rs:1037-1043).
type PluginsDefaultConfig struct {
	// Enabled is the activation default for installed plugins. Rust derives it
	// with `default_enabled`, so an omitted field means true (types.rs:1079).
	Enabled bool
}

// PluginConfig mirrors Rust `PluginConfig` (config/src/types.rs:1112-1142), the
// value of one `[plugins.<id>]` table. Upstream #54424 added the three
// remote-plugin metadata fields; they are stored and round-tripped only, so
// nothing here selects, fetches or provisions a release, and they never touch
// activation. The opt-in activated policy is byte-for-byte the Rust one:
// `allows_plugin` reads `enabled` and nothing else (types.rs:1101-1109).
type PluginConfig struct {
	// Enabled is the explicit activation override. A nil pointer means the field
	// is omitted, so the plugin inherits the installed-plugin default.
	Enabled *bool
	// RemotePluginID is the stable identifier of a ChatGPT-hosted remote plugin
	// (`remote_plugin_id`). Availability is decided by Plugin Service at
	// provisioning time, not by this value.
	RemotePluginID *string
	// ReleaseID pins a remote plugin to a release (`release_id`). Core stores it
	// without selecting or fetching a release.
	ReleaseID *string
	// AutoProvision is provisioning metadata (`auto_provision`). It is not
	// trusted authorization and does not trigger provisioning.
	AutoProvision *bool
}

// PluginsConfigToml mirrors Rust `PluginsConfigToml` (types.rs:1083-1095), the
// `[plugins]` table as Go decodes it out of config.toml.
type PluginsConfigToml struct {
	// Default is `[plugins._default]`, or nil when the table omits it.
	Default *PluginsDefaultConfig
	// Plugins holds every configured per-plugin table keyed by exact
	// `<plugin>@<marketplace>` id. A missing key, and an entry whose `enabled`
	// is omitted, both inherit the default.
	Plugins map[string]PluginConfig
}

// PluginsDefaultConfigKey is the reserved `[plugins._default]` table key. It is
// an activation default, never a plugin id: Rust keeps it out of the per-plugin
// iteration in marketplace_policy.rs:238-243.
const PluginsDefaultConfigKey = "_default"

// AllowsPlugin mirrors Rust `PluginsConfigToml::allows_plugin`
// (types.rs:1059-1072).
func (c PluginsConfigToml) AllowsPlugin(pluginID string) bool {
	if c.Default == nil {
		return true
	}
	if plugin, ok := c.Plugins[pluginID]; ok && plugin.Enabled != nil {
		return *plugin.Enabled
	}
	return c.Default.Enabled
}

// ExplicitEnabled returns the explicit per-plugin `enabled` setting and whether
// the table declares one (Rust `PluginConfig.enabled: Option<bool>`,
// types.rs:1074-1078).
func (c PluginsConfigToml) ExplicitEnabled(pluginID string) (bool, bool) {
	plugin, ok := c.Plugins[pluginID]
	if !ok || plugin.Enabled == nil {
		return false, false
	}
	return *plugin.Enabled, true
}

// PluginConfigFor returns the configured entry for a plugin id, trailing the
// remote-plugin metadata upstream #54424 added to `PluginConfig`. Absent keys
// and non-table values report ok=false.
func (c PluginsConfigToml) PluginConfigFor(pluginID string) (PluginConfig, bool) {
	plugin, ok := c.Plugins[pluginID]
	return plugin, ok
}

// PluginsConfigTomlFromValues parses the `[plugins]` table of a decoded
// config.toml into the activation policy.
func PluginsConfigTomlFromValues(values map[string]any) PluginsConfigToml {
	table, ok := values["plugins"].(map[string]any)
	if !ok {
		return PluginsConfigToml{}
	}
	policy := PluginsConfigToml{}
	for id, raw := range table {
		if strings.TrimSpace(id) == PluginsDefaultConfigKey {
			policy.Default = pluginsDefaultConfigFromValue(raw)
			continue
		}
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if policy.Plugins == nil {
			policy.Plugins = map[string]PluginConfig{}
		}
		policy.Plugins[id] = pluginConfigFromValue(entry)
	}
	return policy
}

// pluginConfigFromValue decodes one `[plugins.<id>]` table into PluginConfig,
// mirroring Rust `PluginConfig` (config/src/types.rs:1112-1142). Every table
// yields an entry, exactly like Rust's `HashMap<String, PluginConfig>` from a
// flattened `[plugins]`, so an entry that sets only remote metadata or only a
// Go bookkeeping key (`installed`, `path`, ...) still keeps its place with an
// omitted `enabled`.
func pluginConfigFromValue(entry map[string]any) PluginConfig {
	var plugin PluginConfig
	if enabled, ok := toBool(entry["enabled"]); ok {
		value := enabled
		plugin.Enabled = &value
	}
	plugin.RemotePluginID = stringPtrIfNotEmpty(stringConfigValue(entry["remote_plugin_id"]))
	plugin.ReleaseID = stringPtrIfNotEmpty(stringConfigValue(entry["release_id"]))
	if autoProvision, ok := toBool(entry["auto_provision"]); ok {
		value := autoProvision
		plugin.AutoProvision = &value
	}
	return plugin
}

// pluginsDefaultConfigFromValue parses the `[plugins._default]` table. A table
// that omits `enabled` still counts as declared, with the Rust default (true).
func pluginsDefaultConfigFromValue(raw any) *PluginsDefaultConfig {
	entry, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	enabled, ok := toBool(entry["enabled"])
	if !ok {
		enabled = true
	}
	return &PluginsDefaultConfig{Enabled: enabled}
}

// applyPluginActivationPolicy narrows effective enablement with the configured
// policy, mirroring loader.rs:184-187 (`enabled.unwrap_or(true)` AND
// `allows_plugin`). It only ever clears the flag, so a detail the configured
// table already resolved keeps its value.
func applyPluginActivationPolicy(details []PluginDetail, policy PluginsConfigToml) {
	for index := range details {
		if !policy.AllowsPlugin(details[index].Summary.ID) {
			details[index].Summary.Enabled = false
		}
	}
}
