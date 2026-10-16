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

// PluginsConfigToml mirrors Rust `PluginsConfigToml` (types.rs:1045-1094), the
// `[plugins]` table as Go decodes it out of config.toml.
type PluginsConfigToml struct {
	// Default is `[plugins._default]`, or nil when the table omits it.
	Default *PluginsDefaultConfig
	// Plugins holds the explicit per-plugin `enabled` overrides keyed by exact
	// `<plugin>@<marketplace>` id. Absent keys, and keys whose setting is
	// omitted, inherit the default.
	Plugins map[string]*bool
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
	if enabled, ok := c.Plugins[pluginID]; ok && enabled != nil {
		return *enabled
	}
	return c.Default.Enabled
}

// ExplicitEnabled returns the explicit per-plugin `enabled` setting and whether
// the table declares one (Rust `PluginConfig.enabled: Option<bool>`,
// types.rs:1074-1078).
func (c PluginsConfigToml) ExplicitEnabled(pluginID string) (bool, bool) {
	enabled, ok := c.Plugins[pluginID]
	if !ok || enabled == nil {
		return false, false
	}
	return *enabled, true
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
		enabled, ok := toBool(entry["enabled"])
		if !ok {
			continue
		}
		if policy.Plugins == nil {
			policy.Plugins = map[string]*bool{}
		}
		value := enabled
		policy.Plugins[id] = &value
	}
	return policy
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
