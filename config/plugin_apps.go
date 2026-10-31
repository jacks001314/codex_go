package config

import (
	"sort"
	"strings"

	codexplugin "codex_go/plugin"
)

// PluginAppsSource is one overlay that contributed to the effective Apps
// configuration, in merge order. Rust #58428 exposes the same prefixes so
// field origins can be projected onto apps.* paths.
type PluginAppsSource struct {
	Prefix string
	Apps   map[string]any
}

// pluginAppsOverlays mirrors Rust apps_config_overlays (#58428). Plugin app
// settings apply connector-wide only for plugins enabled by configuration.
// Plugins are visited in reverse name order so earlier names win conflicts,
// and the explicit top-level apps table is applied last.
func pluginAppsOverlays(values map[string]any) []PluginAppsSource {
	var overlays []PluginAppsSource
	plugins, _ := values["plugins"].(map[string]any)
	policy := codexplugin.PluginsConfigTomlFromValues(values)
	names := make([]string, 0, len(plugins))
	for name := range plugins {
		if strings.TrimSpace(name) == codexplugin.PluginsDefaultConfigKey {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for i := len(names) - 1; i >= 0; i-- {
		name := names[i]
		entry, ok := plugins[name].(map[string]any)
		if !ok {
			continue
		}
		if configured, present := policy.ExplicitEnabled(name); present && !configured {
			continue
		}
		if !policy.AllowsPlugin(name) {
			continue
		}
		apps, ok := entry["apps"].(map[string]any)
		if !ok || len(apps) == 0 {
			continue
		}
		cloned := make(map[string]any, len(apps))
		for key, value := range apps {
			if key == "_default" {
				continue
			}
			cloned[key] = value
		}
		if len(cloned) == 0 {
			continue
		}
		overlays = append(overlays, PluginAppsSource{Prefix: "plugins." + name + ".apps", Apps: cloned})
	}
	if topLevel, ok := values["apps"].(map[string]any); ok {
		overlays = append(overlays, PluginAppsSource{Prefix: "apps", Apps: topLevel})
	}
	return overlays
}

// mergePluginApps applies the overlays to values["apps"]. It reports false when
// no overlay exists, leaving the table absent like Rust apps_config_from_layer_stack.
func mergePluginApps(values map[string]any) bool {
	overlays := pluginAppsOverlays(values)
	if len(overlays) == 0 {
		return false
	}
	merged := map[string]any{}
	for _, overlay := range overlays {
		mergeAppTables(merged, overlay.Apps)
	}
	if len(merged) == 0 {
		return false
	}
	values["apps"] = merged
	return true
}

func mergeAppTables(dst map[string]any, src map[string]any) {
	for key, value := range src {
		if srcTable, ok := value.(map[string]any); ok {
			if dstTable, ok := dst[key].(map[string]any); ok {
				mergeAppTables(dstTable, srcTable)
				continue
			}
			dst[key] = cloneAppTable(srcTable)
			continue
		}
		dst[key] = value
	}
}

func cloneAppTable(src map[string]any) map[string]any {
	cloned := make(map[string]any, len(src))
	mergeAppTables(cloned, src)
	return cloned
}

// projectPluginAppsOrigins mirrors Rust apps_config_origins_from_layer_stack:
// existing apps.* origins are replaced by the origins of the layers that
// contributed each overlay path, in overlay order.
func projectPluginAppsOrigins(values map[string]any, origins map[string]LayerMetadata) {
	if origins == nil {
		return
	}
	overlays := pluginAppsOverlays(values)
	source := make(map[string]LayerMetadata, len(origins))
	for path, metadata := range origins {
		source[path] = metadata
	}
	for path := range origins {
		if strings.HasPrefix(path, "apps.") {
			delete(origins, path)
		}
	}
	for _, overlay := range overlays {
		prefix := overlay.Prefix + "."
		for path, metadata := range source {
			field, ok := strings.CutPrefix(path, prefix)
			if !ok {
				continue
			}
			if overlay.Prefix != "apps" && strings.HasPrefix(field, "_default.") {
				continue
			}
			origins["apps."+field] = metadata
		}
	}
}
