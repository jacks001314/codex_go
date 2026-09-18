package mcp

// mcpReadOnlyToolsMetaKey mirrors Rust
// rmcp-client::READ_ONLY_TOOLS_META_KEY.
const mcpReadOnlyToolsMetaKey = "openai/readOnly"

// applyMCPReadOnlyToolsMeta mirrors Rust
// RmcpClient::apply_read_only_tools_meta: a read-only connection asks the
// server to filter discovery and re-check invocation for every tool request.
// The marker is forced to true, overriding a caller-supplied false, while other
// metadata, pagination, and arguments are preserved.
func applyMCPReadOnlyToolsMeta(params map[string]any, config *ServerConfig) {
	if params == nil || config == nil || !config.RequiresReadOnlyTools {
		return
	}
	meta, _ := params["_meta"].(map[string]any)
	cloned := make(map[string]any, len(meta)+1)
	for key, value := range meta {
		cloned[key] = value
	}
	cloned[mcpReadOnlyToolsMetaKey] = true
	params["_meta"] = cloned
}

// mcpListParamsForCursorWithConfig is the tools/list parameter builder: it adds
// the read-only marker on top of Rust's `PaginatedRequestParams`.
func mcpListParamsForCursorWithConfig(config *ServerConfig, cursor *string) map[string]any {
	params := mcpListParamsForCursor(cursor)
	applyMCPReadOnlyToolsMeta(params, config)
	return params
}

// mcpToolCallParams builds a tools/call request the way Rust's
// CallToolRequestParams does - name, arguments, and the optional `_meta` - with
// the connection's read-only policy applied. Both transports share it so the
// policy cannot diverge between them.
func mcpToolCallParams(config *ServerConfig, tool string, arguments any, meta any) map[string]any {
	params := map[string]any{
		"name":      tool,
		"arguments": arguments,
	}
	if meta != nil {
		params["_meta"] = meta
	}
	applyMCPReadOnlyToolsMeta(params, config)
	return params
}
