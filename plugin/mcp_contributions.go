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
		configs := readPluginMCPServerConfigs(root)
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

func readPluginMCPServerConfigs(pluginRoot string) map[string]map[string]any {
	pluginRoot = strings.TrimSpace(pluginRoot)
	if pluginRoot == "" {
		return nil
	}
	for _, name := range []string{"mcp.json", ".mcp.json"} {
		data, err := os.ReadFile(filepath.Join(pluginRoot, name))
		if err != nil {
			continue
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
