package mcp

import (
	"sort"
	"strings"

	managedconfig "codex_go/config"
)

// PluginAttribution mirrors Rust's McpPluginAttribution and stores plugin identity
// retained with an MCP registration for tool attribution.
type PluginAttribution struct {
	PluginID    string
	DisplayName string
	HostRoot    string
}

// CatalogSource mirrors Rust's McpServerSource and identifies where a server
// registration came from, which determines its priority tier.
type CatalogSource string

const (
	CatalogSourcePlugin         CatalogSource = "plugin"
	CatalogSourceSelectedPlugin CatalogSource = "selected_plugin"
	CatalogSourceConfig         CatalogSource = "config"
	CatalogSourceCompatibility  CatalogSource = "compatibility"
	CatalogSourceExtension      CatalogSource = "extension"
)

// CatalogSourcePriority returns the priority tier for a source.
// Lower tier number = lower priority (loses to higher tiers).
// The priority order (highest wins) is: Extension > Compatibility > Config > SelectedPlugin > Plugin.
func CatalogSourcePriority(source CatalogSource) int {
	switch source {
	case CatalogSourcePlugin:
		return 0
	case CatalogSourceSelectedPlugin:
		return 1
	case CatalogSourceConfig:
		return 2
	case CatalogSourceCompatibility:
		return 3
	case CatalogSourceExtension:
		return 4
	default:
		return -1
	}
}

// DisabledRegistrationIsNameVeto returns whether a disabled registration from
// this source acts as a name-level veto that persists across later runtime overlays.
// Mirrors Rust's McpServerSource::disabled_registration_is_name_veto.
func DisabledRegistrationIsNameVeto(source CatalogSource) bool {
	// A selected plugin's policy applies to its registration, not to a higher runtime source
	// that happens to use the same logical server name.
	return source != CatalogSourceSelectedPlugin
}

// CatalogAction represents a registration or removal action on the MCP catalog.
type CatalogAction struct {
	Name              string
	Source            CatalogSource
	PluginAttribution *PluginAttribution // optional plugin identity for tool attribution
	Config            ServerConfig
	RegistrationOrder int  // tie-breaker within same tier
	Remove            bool // true = removal action
	// CredentialPolicy is the credential authority of this registration: host
	// configuration and host plugins allow host fallback, executor-discovered
	// declarations are confined to the executor (Rust #48143).
	CredentialPolicy CredentialPolicy
}

// ResolvedServer pairs an MCP server config with its winning source and optional plugin attribution.
type ResolvedServer struct {
	Source            CatalogSource
	PluginAttribution *PluginAttribution
	Config            ServerConfig
	CredentialPolicy  CredentialPolicy
}

// CatalogConflict represents a same-tier name collision and the final outcome.
type CatalogConflict struct {
	Name      string
	Outcome   CatalogAction
	Contender []CatalogAction
}

// ResolvedCatalog is the immutable result of MCP registration resolution.
type ResolvedCatalog struct {
	Servers             map[string]ResolvedServer
	Conflicts           []CatalogConflict
	DisabledServerNames map[string]bool // name-level vetoes for subsequent builds
	// RequirementsDisabledServerNames retains managed-requirements denials for
	// subsequent builds (Rust #56266). Only a trusted host-owned Apps
	// registration may replace one of these names.
	RequirementsDisabledServerNames map[string]string
}

// ResolveCatalog resolves MCP server registrations from multiple sources into
// a single winning set, following Rust's priority tiers:
//
//	Extension > Compatibility > Config > SelectedPlugin > Plugin
//
// Within the same tier, the action with the highest RegistrationOrder wins.
// When a source's disabled registration acts as a name-level veto, later sources
// cannot re-enable the same name unless the disabled source was SelectedPlugin.
func ResolveCatalog(actions []CatalogAction) *ResolvedCatalog {
	return ResolveCatalogWithState(nil, nil, actions)
}

// ResolveCatalogWithDisabled resolves MCP registrations with an initial set of
// disabled server names (name-level vetoes from higher-priority tiers).
func ResolveCatalogWithDisabled(initialDisabled map[string]bool, actions []CatalogAction) *ResolvedCatalog {
	return ResolveCatalogWithState(initialDisabled, nil, actions)
}

// ResolveCatalogWithState resolves MCP registrations with the state carried from
// a previous build: disabled server names and managed-requirements denials
// (Rust #56266).
func ResolveCatalogWithState(initialDisabled map[string]bool, initialRequirementsDisabled map[string]string, actions []CatalogAction) *ResolvedCatalog {
	disabled := make(map[string]bool, len(initialDisabled))
	for name := range initialDisabled {
		disabled[name] = true
	}
	requirementsDisabled := make(map[string]string, len(initialRequirementsDisabled))
	for name, reason := range initialRequirementsDisabled {
		requirementsDisabled[name] = reason
	}

	// Sort actions by priority tier ascending, then by registration order descending
	// within tier. Last action wins per name within each tier.
	sort.SliceStable(actions, func(i, j int) bool {
		pi := CatalogSourcePriority(actions[i].Source)
		pj := CatalogSourcePriority(actions[j].Source)
		if pi != pj {
			return pi < pj
		}
		// Within same tier, higher registration order = later/last = wins
		return actions[i].RegistrationOrder < actions[j].RegistrationOrder
	})

	// Track conflicts: same name, same tier
	type tierKey struct {
		name string
		tier int
	}
	byTier := map[tierKey][]CatalogAction{}
	for _, action := range actions {
		key := tierKey{name: action.Name, tier: CatalogSourcePriority(action.Source)}
		byTier[key] = append(byTier[key], action)
	}

	// Build winners per name across all tiers. Since actions are sorted ascending,
	// each subsequent action overwrites the previous winner for the same name.
	winners := map[string]CatalogAction{}
	for _, action := range actions {
		winners[action.Name] = action
	}

	// Collect conflicts (same name, same tier, more than one action).
	var conflicts []CatalogConflict
	for _, tierActions := range byTier {
		if len(tierActions) < 2 {
			continue
		}
		outcome, exists := winners[tierActions[0].Name]
		if !exists {
			continue
		}
		conflicts = append(conflicts, CatalogConflict{
			Name:      outcome.Name,
			Outcome:   outcome,
			Contender: append([]CatalogAction(nil), tierActions...),
		})
	}

	// Build final server map, applying removal actions and disabled vetos.
	servers := make(map[string]ResolvedServer, len(winners))
	for name, action := range winners {
		if action.Remove {
			continue
		}
		config := action.Config
		persistDisabled := DisabledRegistrationIsNameVeto(action.Source)
		if !config.Enabled && persistDisabled {
			switch reason := strings.TrimSpace(config.DisabledReason); {
			case reason == managedconfig.MCPDisabledByRequirements:
				requirementsDisabled[name] = reason
			case reason == managedconfig.MCPDisabledByExplicitAndRequirements:
				requirementsDisabled[name] = reason
				disabled[name] = true
			default:
				disabled[name] = true
			}
		}
		if disabled[name] {
			config.Enabled = false
		}
		// Only trusted Apps may ignore a previous registration's requirements
		// denial; keep it so a later custom replacement is still blocked.
		if !isHostOwnedAppsSource(name, action.Source, &config) {
			if _, denied := requirementsDisabled[name]; denied {
				disableServerConfigByRequirements(&config)
			}
		}
		servers[name] = ResolvedServer{
			Source:            action.Source,
			PluginAttribution: action.PluginAttribution,
			Config:            config,
			CredentialPolicy:  EffectiveCredentialPolicy(action.CredentialPolicy),
		}
	}

	return &ResolvedCatalog{
		Servers:                         servers,
		Conflicts:                       conflicts,
		DisabledServerNames:             disabled,
		RequirementsDisabledServerNames: requirementsDisabled,
	}
}

// isHostOwnedAppsSource mirrors McpServerSource::is_host_owned_apps for a
// catalog action: only the compatibility source owns the Apps registration.
func isHostOwnedAppsSource(name string, source CatalogSource, config *ServerConfig) bool {
	if strings.TrimSpace(name) != CodexAppsServerName || config == nil || !config.IsLocalEnvironment() {
		return false
	}
	return source == CatalogSourceCompatibility
}

// SourceFromRegistration derives the CatalogSource from a ServerRegistration.
func SourceFromRegistration(reg *ServerRegistration) CatalogSource {
	if reg == nil {
		return CatalogSourceConfig
	}
	source := strings.ToLower(strings.TrimSpace(reg.Source))
	switch source {
	case "plugin":
		if reg.SelectionOrder > 0 {
			return CatalogSourceSelectedPlugin
		}
		return CatalogSourcePlugin
	case "selected_plugin", "selectedplugin":
		return CatalogSourceSelectedPlugin
	case "config":
		return CatalogSourceConfig
	case "compatibility":
		return CatalogSourceCompatibility
	case "extension":
		return CatalogSourceExtension
	default:
		if reg.PluginID != "" {
			return CatalogSourcePlugin
		}
		if reg.ContributorID != "" {
			if strings.Contains(strings.ToLower(reg.ContributorID), "compat") || reg.ContributorID == legacyCodexAppsRegistrationID {
				return CatalogSourceCompatibility
			}
			return CatalogSourceExtension
		}
		return CatalogSourceConfig
	}
}

// PluginAttributionFromRegistration extracts plugin attribution from a ServerRegistration.
func PluginAttributionFromRegistration(reg *ServerRegistration) *PluginAttribution {
	if reg == nil || reg.PluginID == "" {
		return nil
	}
	return &PluginAttribution{
		PluginID:    reg.PluginID,
		DisplayName: reg.PluginDisplayName,
		HostRoot:    reg.PluginHostRoot,
	}
}
