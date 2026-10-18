package mcp

// cachedCatalogStartupTools returns the cached catalog a server may expose
// before its live connection is ready. The catalog must still pass the
// server's tool filter and contain a model-visible tool.
func (s *MCPService) cachedCatalogStartupTools(name string, config *ServerConfig, status MCPServerStatus) ([]MCPToolInfo, bool) {
	if s == nil || len(status.Tools) == 0 || !s.retainedToolCatalogUsable(name) {
		return nil, false
	}
	tools := FilterMCPTools(status.Tools, ToolFilterFromServerConfig(config))
	if IsCodexAppsMCPServerName(name) {
		return append([]MCPToolInfo(nil), tools...), true
	}
	visible := make([]MCPToolInfo, 0, len(tools))
	for i := range tools {
		if mcpToolIsModelVisible(&tools[i]) {
			visible = append(visible, tools[i])
		}
	}
	if len(visible) == 0 {
		return nil, false
	}
	return visible, true
}

// mcpToolIsModelVisible mirrors Rust's tool_is_model_visible: tools without
// _meta.ui.visibility stay visible, while a visibility array must explicitly
// include "model".
func mcpToolIsModelVisible(tool *MCPToolInfo) bool {
	if tool == nil {
		return false
	}
	if visible := ToolModelVisible(tool); visible != nil && !*visible {
		return false
	}
	meta := metadataMap(tool.Meta)
	ui := metadataMap(meta["ui"])
	raw, ok := ui["visibility"]
	if !ok {
		return true
	}
	switch visibility := raw.(type) {
	case []any:
		for _, target := range visibility {
			if value, ok := target.(string); ok && value == "model" {
				return true
			}
		}
		return false
	case []string:
		for _, target := range visibility {
			if target == "model" {
				return true
			}
		}
		return false
	default:
		return true
	}
}
