package plugin

import (
	"strings"
)

// AppsRouteAvailable checks whether apps routing is available for the current session.
// Apps only route when connected to the ChatGPT backend (not local-only auth).
func AppsRouteAvailable(authMode string) bool {
	authMode = strings.TrimSpace(authMode)
	return authMode != "" && usesCodexBackend(authMode)
}

func usesCodexBackend(authMode string) bool {
	switch strings.ToLower(authMode) {
	case "chatgpt", "oauth", "bearer", "api_key":
		return true
	default:
		return false
	}
}

// ApplyAppMcpRoutingPolicy adjusts MCP server names when apps routing is active.
//
// When apps routing is available AND a plugin declares both MCP servers and
// app connectors with overlapping names, the MCP server names that overlap
// with app declarations are removed. This prevents dual-routing of the same
// capability through both MCP and App channels.
//
// When apps routing is unavailable, the apps list is cleared entirely.
//
// Returns the filtered MCP server names and apps.
func ApplyAppMcpRoutingPolicy(
	authMode string,
	// isPluginActive bool,
	mcpServerNames []string,
	apps []AppSummary,
	appTemplates []AppTemplateSummary,
) ([]string, []AppSummary) {
	routable := AppsRouteAvailable(authMode)

	if !routable {
		// When apps are not routable, clear all app declarations
		return mcpServerNames, nil
	}

	// Collect app connector IDs that overlap with MCP server names
	appConnectorNames := map[string]bool{}
	for _, app := range apps {
		if id := strings.TrimSpace(app.ID); id != "" {
			appConnectorNames[strings.ToLower(id)] = true
		}
		if display := strings.TrimSpace(app.DisplayName); display != "" {
			appConnectorNames[strings.ToLower(display)] = true
		}
	}
	for _, t := range appTemplates {
		if t.CanonicalConnectorID != nil {
			appConnectorNames[strings.ToLower(strings.TrimSpace(*t.CanonicalConnectorID))] = true
		}
		for _, id := range t.MaterializedAppIDs {
			if id := strings.TrimSpace(id); id != "" {
				appConnectorNames[strings.ToLower(id)] = true
			}
		}
	}

	// Remove MCP server names that overlap with app connector names
	filteredMCPServers := make([]string, 0, len(mcpServerNames))
	for _, serverName := range mcpServerNames {
		if !appConnectorNames[strings.ToLower(strings.TrimSpace(serverName))] {
			filteredMCPServers = append(filteredMCPServers, serverName)
		}
	}

	return filteredMCPServers, apps
}
