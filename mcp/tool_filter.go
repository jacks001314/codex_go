package mcp

import "strings"

// ToolFilter mirrors Rust's ToolFilter for per-server tool allowlist/denylist.
//
// A tool is allowed to be used if both are true:
//  1. enabled is nil (no allowlist is set) or the tool is explicitly enabled.
//  2. The tool is not explicitly disabled.
type ToolFilter struct {
	Enabled  map[string]bool `json:"enabled,omitempty"`
	Disabled map[string]bool `json:"disabled,omitempty"`
}

// NewToolFilter creates a ToolFilter from enabled/disabled string slices.
func NewToolFilter(enabled []string, disabled []string) *ToolFilter {
	filter := &ToolFilter{}
	if len(enabled) > 0 {
		filter.Enabled = make(map[string]bool, len(enabled))
		for _, name := range enabled {
			name = strings.TrimSpace(name)
			if name != "" {
				filter.Enabled[name] = true
			}
		}
	}
	if len(disabled) > 0 {
		filter.Disabled = make(map[string]bool, len(disabled))
		for _, name := range disabled {
			name = strings.TrimSpace(name)
			if name != "" {
				filter.Disabled[name] = true
			}
		}
	}
	if len(filter.Enabled) == 0 && len(filter.Disabled) == 0 {
		return nil
	}
	return filter
}

// Allows returns true if the tool is allowed by this filter.
func (f *ToolFilter) Allows(toolName string) bool {
	if f == nil {
		return true
	}
	if len(f.Enabled) > 0 && !f.Enabled[toolName] {
		return false
	}
	if f.Disabled[toolName] {
		return false
	}
	return true
}

// ToolFilterFromServerConfig builds a ToolFilter from a ServerConfig's
// EnabledTools/DisabledTools fields.
func ToolFilterFromServerConfig(config *ServerConfig) *ToolFilter {
	if config == nil {
		return nil
	}
	return NewToolFilter(config.EnabledTools, config.DisabledTools)
}

// FilterTools applies a ToolFilter to a slice of RuntimeToolInfo,
// removing tools that are not allowed by the filter.
func FilterTools(tools []RuntimeToolInfo, filter *ToolFilter) []RuntimeToolInfo {
	if filter == nil {
		return tools
	}
	out := make([]RuntimeToolInfo, 0, len(tools))
	for i := range tools {
		if filter.Allows(tools[i].Tool.Name) {
			out = append(out, tools[i])
		}
	}
	return out
}

// FilterMCPTools applies a ToolFilter to a slice of MCPToolInfo,
// removing tools that are not allowed by the filter.
func FilterMCPTools(tools []MCPToolInfo, filter *ToolFilter) []MCPToolInfo {
	if filter == nil {
		return tools
	}
	out := make([]MCPToolInfo, 0, len(tools))
	for i := range tools {
		if filter.Allows(tools[i].Name) {
			out = append(out, tools[i])
		}
	}
	return out
}
