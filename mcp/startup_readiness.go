package mcp

import "strings"

// mcpServerCanDeferStdioStartup mirrors Rust #60039's
// McpServerConfig::can_defer_stdio_startup: only stdio servers are eligible, and a
// required server defers only when catalog readiness already satisfies startup.
// Explicit reconnects and required live startup stay eager.
func mcpServerCanDeferStdioStartup(config *ServerConfig) bool {
	if config == nil {
		return false
	}
	if strings.TrimSpace(config.URL) != "" || strings.TrimSpace(config.Command) == "" {
		return false
	}
	return !config.Required || config.StartupReadiness.Effective() == MCPStartupReadinessCatalog
}

// cachedCatalogStartupTools returns the cached catalog a server may expose
// before its live connection is ready. Two sources qualify, in order of
// precedence:
//
//  1. the catalog this chat already published on the server status;
//  2. the process-scoped tool catalog cache, which is what lets a *new* chat
//     satisfy startup without launching the server (Rust ToolCatalogCache).
//
// Either way the catalog must still pass the server's tool filter and contain a
// model-visible tool.
func (s *MCPService) cachedCatalogStartupTools(name string, config *ServerConfig, status MCPServerStatus) ([]MCPToolInfo, bool) {
	if s == nil {
		return nil, false
	}
	if len(status.Tools) > 0 {
		// A published local catalog is authoritative while it is retained. When a
		// tool-list notification is pending the service must re-list instead of
		// answering from either source, so the process cache must not fill in here.
		if !s.retainedToolCatalogUsable(name) {
			return nil, false
		}
		if tools, ok := usableCachedStartupTools(name, config, status.Tools); ok {
			return tools, true
		}
	}
	identity, ok := mcpToolCatalogIdentity(name, config)
	if !ok {
		return nil, false
	}
	entry := s.toolCatalogCacheStore().Entry(identity)
	if entry == nil {
		return nil, false
	}
	tools, ok := entry.currentTools(s.mcpNow(), config.ProtocolMode.usesModernMCPLifecycle())
	if !ok {
		return nil, false
	}
	return usableCachedStartupTools(name, config, tools)
}

// usableCachedStartupTools filters a candidate catalog down to what this server
// may advertise to the model.
func usableCachedStartupTools(name string, config *ServerConfig, catalog []MCPToolInfo) ([]MCPToolInfo, bool) {
	tools := FilterMCPTools(catalog, ToolFilterFromServerConfig(config))
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
