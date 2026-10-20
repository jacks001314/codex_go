package plugin

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"codex_go/execserver"
)

// CatalogFromExecutorSnapshots projects V2 discovery inventories into an inert
// executor plugin catalog without additional I/O (Rust #55301). A failed
// snapshot contributes a warning and no entries; a successful empty snapshot
// establishes an empty inventory.
func CatalogFromExecutorSnapshots(snapshots []execserver.ExecutorCapabilityDiscoveryV2Snapshot) *PluginCatalog {
	catalog := &PluginCatalog{}
	for _, snapshot := range snapshots {
		if strings.TrimSpace(snapshot.Err) != "" {
			catalog.Warnings = append(catalog.Warnings, fmt.Sprintf(
				"could not discover plugins in environment `%s`; skipping its plugin inventory: %s",
				snapshot.EnvironmentID, snapshot.Err,
			))
			continue
		}
		if snapshot.Response == nil {
			continue
		}
		catalog.Warnings = append(catalog.Warnings, snapshot.Response.Warnings...)
		for _, discovered := range snapshot.Response.Plugins {
			entry, warnings := executorPluginCatalogEntry(snapshot.EnvironmentID, discovered)
			catalog.Entries = append(catalog.Entries, entry)
			catalog.Warnings = append(catalog.Warnings, warnings...)
		}
	}
	return catalog
}

// MCPServerContributions projects catalog MCP declarations into runtime
// contributions. For a merged plugin the first executor installation runs the
// declarations; additional installations remain in the catalog for
// attribution and future surfaces.
func (c *PluginCatalog) MCPServerContributions(configured map[string]PluginConfig) []MCPServerContribution {
	if c == nil {
		return nil
	}
	out := []MCPServerContribution{}
	order := 0
	for _, entry := range c.Entries {
		if !entry.IsEnabled(configured) || len(entry.MCPServers) == 0 {
			continue
		}
		var location *PluginSourceLocation
		for index := range entry.Locations {
			if entry.Locations[index].Kind == PluginSourceExecutor {
				location = &entry.Locations[index]
				break
			}
		}
		if location == nil {
			continue
		}
		names := make([]string, 0, len(entry.MCPServers))
		for name := range entry.MCPServers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			var config map[string]any
			if err := json.Unmarshal([]byte(entry.MCPServers[name]), &config); err != nil {
				continue
			}
			auth, _ := config["auth"].(string)
			if strings.EqualFold(strings.TrimSpace(auth), "ema_auth") {
				continue
			}
			order++
			out = append(out, MCPServerContribution{
				Name:              name,
				PluginID:          location.PluginID,
				PluginDisplayName: entry.DisplayName,
				PluginRoot:        location.Root,
				Config:            config,
				Order:             order,
			})
		}
	}
	return out
}

// CapabilitySummaries projects enabled catalog entries into selected-plugin
// snapshots, carrying the executor package key and remote identity together
// (Rust #55301).
func (c *PluginCatalog) CapabilitySummaries(configured map[string]PluginConfig) []CapabilitySummary {
	if c == nil {
		return nil
	}
	out := make([]CapabilitySummary, 0, len(c.Entries))
	for _, entry := range c.Entries {
		if !entry.IsEnabled(configured) {
			continue
		}
		configName := ""
		for _, location := range entry.Locations {
			if location.Kind == PluginSourceExecutor && strings.TrimSpace(location.PluginID) != "" {
				configName = location.PluginID
				break
			}
		}
		if configName == "" {
			configName = entry.ID.AsString()
		}
		servers := make([]string, 0, len(entry.MCPServers))
		for name := range entry.MCPServers {
			servers = append(servers, name)
		}
		sort.Strings(servers)
		out = append(out, CapabilitySummary{
			Name:           configName,
			ConfigName:     configName,
			DisplayName:    entry.DisplayName,
			RemotePluginID: entry.ID.RemotePluginID,
			MCPServers:     servers,
			AppConnectors:  append([]string(nil), entry.ConnectorIDs...),
		})
	}
	return out
}

// MergeCapabilitySummaries appends catalog capabilities whose identity is not
// already represented, preserving the cloud summaries' richer metadata.
func MergeCapabilitySummaries(base []CapabilitySummary, extra []CapabilitySummary) []CapabilitySummary {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base))
	for _, capability := range base {
		if capability.RemotePluginID != "" {
			seen["remote:"+capability.RemotePluginID] = true
		}
		if capability.ConfigName != "" {
			seen["local:"+capability.ConfigName] = true
		}
	}
	merged := append([]CapabilitySummary(nil), base...)
	for _, capability := range extra {
		if capability.RemotePluginID != "" && seen["remote:"+capability.RemotePluginID] {
			continue
		}
		if capability.ConfigName != "" && seen["local:"+capability.ConfigName] {
			continue
		}
		merged = append(merged, capability)
	}
	return merged
}

func executorPluginCatalogEntry(environmentID string, discovered execserver.ExecutorPlugin) (PluginCatalogEntry, []string) {
	entry := PluginCatalogEntry{
		DisplayName: discovered.DisplayName,
		Version:     discovered.Version,
		MCPServers:  map[string]string{},
		Locations: []PluginSourceLocation{{
			Kind:          PluginSourceExecutor,
			EnvironmentID: environmentID,
			PluginID:      discovered.ID,
			Root:          discovered.Root,
		}},
	}
	if remoteID := strings.TrimSpace(ptrString(discovered.RemotePluginID)); remoteID != "" {
		entry.ID = PluginIdentity{Kind: PluginIdentityRemote, RemotePluginID: remoteID}
	} else {
		entry.ID = PluginIdentity{Kind: PluginIdentityLocal, PluginID: discovered.ID}
	}
	var warnings []string
	if discovered.Files.MCPConfig != nil && strings.TrimSpace(discovered.Files.MCPConfig.Contents) != "" {
		servers, err := parsePluginMCPDeclarations(discovered.Files.MCPConfig.Contents)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("plugin `%s` MCP configuration: %v", discovered.ID, err))
		} else {
			entry.MCPServers = servers
		}
	}
	if discovered.Files.AppsConfig != nil && strings.TrimSpace(discovered.Files.AppsConfig.Contents) != "" {
		connectors, err := parsePluginConnectorIDs(discovered.Files.AppsConfig.Contents)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("plugin `%s` app configuration: %v", discovered.ID, err))
		} else {
			entry.ConnectorIDs = connectors
		}
	}
	return entry, warnings
}

func parsePluginMCPDeclarations(contents string) (map[string]string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(contents), &raw); err != nil {
		return nil, err
	}
	if wrapped, ok := raw["mcpServers"]; ok {
		raw = map[string]json.RawMessage{}
		if err := json.Unmarshal(wrapped, &raw); err != nil {
			return nil, err
		}
	}
	servers := make(map[string]string, len(raw))
	for name, declaration := range raw {
		servers[name] = string(declaration)
	}
	return servers, nil
}

func parsePluginConnectorIDs(contents string) ([]string, error) {
	var value any
	if err := json.Unmarshal([]byte(contents), &value); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var connectors []string
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				if (key == "connectorId" || key == "connector_id") && strings.TrimSpace(stringFromAnyPlugin(child)) != "" {
					id := strings.TrimSpace(stringFromAnyPlugin(child))
					if !seen[id] {
						seen[id] = true
						connectors = append(connectors, id)
					}
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return connectors, nil
}

func stringFromAnyPlugin(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func ptrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
