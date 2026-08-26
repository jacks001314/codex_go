package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type MCPServerContribution struct {
	Name              string
	PluginID          string
	PluginDisplayName string
	PluginRoot        string
	Config            map[string]any
	Order             int
}

func (s *PluginService) EnabledMCPServerContributions() []MCPServerContribution {
	if s == nil {
		return nil
	}
	details := s.materializePluginDetailsBestEffort(s.enabledPluginDetailsSnapshot())
	sort.SliceStable(details, func(i int, j int) bool { return details[i].Summary.ID < details[j].Summary.ID })
	out := []MCPServerContribution{}
	order := 0
	for _, detail := range details {
		root := pluginExecutionRoot(&detail)
		if root == "" || detail.Summary.ID == "" {
			continue
		}
		configs := readPluginMCPServerConfigs(root, s.agentPluginDataRoot(detail.Summary.ID, root))
		// #40363: apply local env_vars from .codex-plugin/plugin.json to matching
		// stdio servers loaded from an Agent Plugin manifest (no-op otherwise).
		applyCodexEnvOverlay(root, configs)
		names := make([]string, 0, len(configs))
		for name := range configs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			order++
			out = append(out, MCPServerContribution{
				Name:              name,
				PluginID:          detail.Summary.ID,
				PluginDisplayName: firstNonEmpty(detail.Summary.DisplayName, detail.Summary.Name, detail.Summary.ID),
				PluginRoot:        root,
				Config:            configs[name],
				Order:             order,
			})
		}
	}
	return out
}

// agentPluginDataRoot returns the plugin runtime data directory used for
// `${PLUGIN_DATA}` expansion, falling back to the plugin root when the service
// has no Codex home or the plugin id cannot be parsed.
func (s *PluginService) agentPluginDataRoot(pluginID string, fallbackRoot string) string {
	if s == nil {
		return fallbackRoot
	}
	s.mu.Lock()
	codexHome := s.codexHome
	s.mu.Unlock()
	if strings.TrimSpace(codexHome) == "" {
		return fallbackRoot
	}
	id, err := ParsePluginId(pluginID)
	if err != nil {
		return fallbackRoot
	}
	return filepath.Join(codexHome, PluginsDataDir, id.PluginName+"-"+id.MarketplaceName)
}

func readPluginMCPServerConfigs(pluginRoot string, pluginDataRoot string) map[string]map[string]any {
	pluginRoot = strings.TrimSpace(pluginRoot)
	if pluginRoot == "" {
		return nil
	}
	for _, name := range []string{"mcp.json", ".mcp.json"} {
		data, err := os.ReadFile(filepath.Join(pluginRoot, name))
		if err != nil {
			continue
		}
		if name == "mcp.json" {
			if outcome, parseErr := ParseAgentPluginMCPConfig(string(data), pluginRoot, pluginDataRoot); parseErr == nil && outcome != nil {
				// Agent Plugins v1 mcp.json: keep valid sibling servers and drop
				// per-server parse errors (best-effort contribution loading).
				return outcome.Servers
			}
		}
		var payload struct {
			MCPServers map[string]map[string]any `json:"mcpServers"`
		}
		if json.Unmarshal(data, &payload) == nil && len(payload.MCPServers) > 0 {
			return payload.MCPServers
		}
	}
	return nil
}
