package plugin

import (
	"sort"
	"strings"
)

// PluginToggle represents a per-plugin feature toggle (enable/disable).
type PluginToggle struct {
	PluginID *PluginId
	Enabled  bool
}

// CollectPluginEnabledCandidates parses configuration values to find per-plugin
// "enabled" toggle changes. It supports three key path patterns:
//
// 1. "plugins.<plugin_id>.enabled" — direct boolean toggle
// 2. "plugins.<plugin_id>" — a table containing "enabled" field
// 3. "plugins" — the full plugins map with per-plugin "enabled" fields
//
// Only entries with boolean "enabled" values are returned.
// Later writes for the same plugin override earlier ones.
func CollectPluginEnabledCandidates(configEdits []ConfigEdit) []PluginToggle {
	// Collect raw toggles indexed by plugin ID
	raw := map[string]bool{}
	for _, edit := range configEdits {
		key := strings.TrimSpace(edit.Key)
		if toggles := parsePluginToggleEdits(key, edit.Value); len(toggles) > 0 {
			for pluginID, enabled := range toggles {
				raw[pluginID] = enabled
			}
		}
	}

	// Convert to sorted list
	toggles := make([]PluginToggle, 0, len(raw))
	for id, enabled := range raw {
		pluginID, err := ParsePluginId(id)
		if err != nil {
			// Invalid plugin ID — skip
			continue
		}
		toggles = append(toggles, PluginToggle{
			PluginID: pluginID,
			Enabled:  enabled,
		})
	}

	sort.SliceStable(toggles, func(i, j int) bool {
		return toggles[i].PluginID.Key() < toggles[j].PluginID.Key()
	})

	return toggles
}

// ConfigEdit represents a single configuration edit with a key path and value.
type ConfigEdit struct {
	Key   string
	Value any
}

// parsePluginToggleEdits parses a config edit key/value pair for plugin toggle semantics.
// Returns a map of plugin ID -> enabled value. Empty map means this edit doesn't contain toggles.
func parsePluginToggleEdits(key string, value any) map[string]bool {
	parts := strings.Split(key, ".")
	if len(parts) == 0 || parts[0] != "plugins" {
		return nil
	}

	switch {
	case len(parts) == 3 && parts[2] == "enabled":
		// plugins.<plugin_id>.enabled
		pluginID := parts[1]
		enabled, ok := toBool(value)
		if !ok {
			return nil
		}
		return map[string]bool{normalizePluginToggleID(pluginID, value): enabled}

	case len(parts) == 2:
		// plugins.<plugin_id> — a table containing "enabled"
		pluginID := parts[1]
		if m, ok := value.(map[string]any); ok {
			if en, ok := toBool(m["enabled"]); ok {
				return map[string]bool{normalizePluginToggleID(pluginID, m): en}
			}
		}
		return nil

	case len(parts) == 1:
		// "plugins" — the full map
		if m, ok := value.(map[string]any); ok {
			result := map[string]bool{}
			for k, v := range m {
				if sub, ok := v.(map[string]any); ok {
					if en, ok := toBool(sub["enabled"]); ok {
						result[normalizePluginToggleID(k, sub)] = en
					}
				}
			}
			return result
		}
	}

	return nil
}

func toBool(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case *bool:
		if v != nil {
			return *v, true
		}
	}
	return false, false
}

// normalizePluginToggleID tries to reconstruct a plugin ID from the value context.
func normalizePluginToggleID(pluginID string, value any) string {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return pluginID
	}
	// If pluginID already contains @, use as-is
	if strings.Contains(pluginID, "@") {
		return pluginID
	}
	// Try to infer marketplace from the value
	if m, ok := value.(map[string]any); ok {
		if marketplace, ok := m["marketplace"].(string); ok && strings.TrimSpace(marketplace) != "" {
			return pluginID + "@" + strings.TrimSpace(marketplace)
		}
	}
	return pluginID
}
