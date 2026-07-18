package features

import (
	"sort"
	"strings"
)

type LegacyFeatureUsage struct {
	Alias   string
	Feature string
}

var legacyFeatureAliases = map[string]string{
	"connectors":                          "apps",
	"enable_experimental_windows_sandbox": "experimental_windows_sandbox",
	"experimental_use_unified_exec_tool":  "unified_exec",
	"request_permissions":                 "exec_permission_approvals",
	"web_search":                          "web_search_request",
	"collab":                              "multi_agent",
	"memory_tool":                         "memories",
	"telepathy":                           "chronicle",
	"codex_hooks":                         "hooks",
	"imagegenext":                         "image_generation",
}

var ignoredFeatureKeys = map[string]bool{
	"tui_app_server":                     true,
	"undo":                               true,
	"js_repl":                            true,
	"js_repl_tools_only":                 true,
	"remote_control":                     true,
	"apply_patch_freeform":               true,
	"tool_search":                        true,
	"tool_search_always_defer_mcp_tools": true,
	"apps_mcp_path_override":             true,
	"image_detail_original":              true,
	"resize_all_images":                  true,
	"plugin_hooks":                       true,
	"skill_env_var_dependency_prompt":    true,
	"terminal_resize_reflow":             true,
}

// CanonicalKey returns the canonical feature key for a canonical key or accepted
// legacy alias.
func CanonicalKey(key string) (string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	if _, ok := byKey()[key]; ok {
		return key, true
	}
	canonical, ok := legacyFeatureAliases[key]
	return canonical, ok
}

func ResolveSettings(raw map[string]any) (map[string]bool, []LegacyFeatureUsage) {
	settings := map[string]bool{}
	if len(raw) == 0 {
		return settings, nil
	}
	usages := map[string]LegacyFeatureUsage{}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		enabled, ok := featureEnabledValue(raw[key])
		if !ok {
			continue
		}
		if alias, canonical, ok := forcedLegacyUsageForKey(key); ok {
			recordLegacyUsage(usages, alias, canonical)
		}
		if ignoredFeatureKeys[key] {
			continue
		}
		canonical, ok := CanonicalKey(key)
		if !ok {
			continue
		}
		if key != canonical {
			recordLegacyUsage(usages, key, canonical)
			if rawCanonical, canonicalPresent := raw[canonical]; canonicalPresent {
				if _, ok := featureEnabledValue(rawCanonical); ok {
					continue
				}
			}
		}
		settings[canonical] = enabled
	}
	normalizeSettings(settings)
	return settings, sortedLegacyUsages(usages)
}

func featureEnabledValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case map[string]any:
		enabled, ok := typed["enabled"].(bool)
		return enabled, ok
	default:
		return false, false
	}
}

func forcedLegacyUsageForKey(key string) (string, string, bool) {
	switch key {
	case "web_search_request":
		return "features.web_search_request", "web_search_request", true
	case "web_search_cached":
		return "features.web_search_cached", "web_search_cached", true
	case "use_legacy_landlock":
		return "features.use_legacy_landlock", "use_legacy_landlock", true
	default:
		return "", "", false
	}
}

func recordLegacyUsage(usages map[string]LegacyFeatureUsage, alias string, canonical string) {
	if alias == "" || canonical == "" {
		return
	}
	key := alias + "\x00" + canonical
	usages[key] = LegacyFeatureUsage{Alias: alias, Feature: canonical}
}

func sortedLegacyUsages(usages map[string]LegacyFeatureUsage) []LegacyFeatureUsage {
	if len(usages) == 0 {
		return nil
	}
	out := make([]LegacyFeatureUsage, 0, len(usages))
	for _, usage := range usages {
		out = append(out, usage)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Alias == out[j].Alias {
			return out[i].Feature < out[j].Feature
		}
		return out[i].Alias < out[j].Alias
	})
	return out
}

func normalizeSettings(settings map[string]bool) {
	if settings["enable_fanout"] {
		settings["multi_agent"] = true
	}
	if settings["code_mode_only"] {
		settings["code_mode"] = true
	}
}
