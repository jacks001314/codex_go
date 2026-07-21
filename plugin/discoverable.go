package plugin

import (
	"sort"
	"strings"
)

// ToolSuggestDiscoverablePluginAllowlist is the curated list of known-good plugins
// that appear in the tool-suggest discoverable fallback. Mirrors Rust's
// TOOL_SUGGEST_DISCOVERABLE_PLUGIN_ALLOWLIST with 30 entries.
//
// The list includes plugins from three marketplaces:
//   - openai-curated (14 entries): plugins shipped with the curated repo
//   - openai-curated-remote (14 entries): mirrors of the above for the remote catalog
//   - openai-bundled (2 entries): chrome, computer-use
var ToolSuggestDiscoverablePluginAllowlist = []string{
	// openai-curated (14 plugins)
	"github@openai-curated",
	"notion@openai-curated",
	"slack@openai-curated",
	"gmail@openai-curated",
	"google-calendar@openai-curated",
	"google-drive@openai-curated",
	"openai-developers@openai-curated",
	"canva@openai-curated",
	"teams@openai-curated",
	"sharepoint@openai-curated",
	"outlook-email@openai-curated",
	"outlook-calendar@openai-curated",
	"linear@openai-curated",
	"figma@openai-curated",

	// openai-curated-remote (14 plugins, mirrors of the above)
	"github@openai-curated-remote",
	"notion@openai-curated-remote",
	"slack@openai-curated-remote",
	"gmail@openai-curated-remote",
	"google-calendar@openai-curated-remote",
	"google-drive@openai-curated-remote",
	"openai-developers@openai-curated-remote",
	"canva@openai-curated-remote",
	"teams@openai-curated-remote",
	"sharepoint@openai-curated-remote",
	"outlook-email@openai-curated-remote",
	"outlook-calendar@openai-curated-remote",
	"linear@openai-curated-remote",
	"figma@openai-curated-remote",

	// openai-bundled (2 plugins)
	"chrome@openai-bundled",
	"computer-use@openai-bundled",
}

// allowlistSet is a lazily initialized map for fast lookups.
var allowlistSet map[string]bool

func initAllowlistSet() {
	if allowlistSet != nil {
		return
	}
	allowlistSet = make(map[string]bool, len(ToolSuggestDiscoverablePluginAllowlist))
	for _, key := range ToolSuggestDiscoverablePluginAllowlist {
		allowlistSet[strings.TrimSpace(key)] = true
	}
}

// IsToolSuggestFallbackPlugin checks whether a plugin ID is in the curated allowlist
// that serves as the tool-suggest discoverable fallback.
//
// It also handles cross-referencing: plugins from the "openai-api-curated" marketplace
// are checked against their "openai-curated" equivalent on the allowlist.
func IsToolSuggestFallbackPlugin(pluginID *PluginId) bool {
	if pluginID == nil {
		return false
	}

	initAllowlistSet()

	// Direct lookup
	if allowlistSet[pluginID.Key()] {
		return true
	}

	// Cross-reference: openai-api-curated -> openai-curated
	if pluginID.MarketplaceName == "openai-api-curated" {
		crossID := pluginID.PluginName + "@openai-curated"
		if allowlistSet[crossID] {
			return true
		}
	}

	return false
}

// CuratedPluginAllowlist returns a sorted, deduplicated list of unique plugin names
// from the curated allowlist (without marketplace suffix).
func CuratedPluginAllowlist() []string {
	initAllowlistSet()
	seen := map[string]bool{}
	var names []string
	for _, key := range ToolSuggestDiscoverablePluginAllowlist {
		name, _, found := strings.Cut(key, "@")
		if found && name != "" {
			lname := strings.ToLower(strings.TrimSpace(name))
			if !seen[lname] {
				seen[lname] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}
