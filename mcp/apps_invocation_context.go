package mcp

// AppsInvocationContext returns the connector-owned opaque pin a Codex Apps
// tool declaration carries in its `_codex_apps` metadata. Rust forwards the
// member on every Apps call (mcp_tool_call.rs apps_invocation_context); the
// connector owns the contents, so this package only projects and clones it.
func AppsInvocationContext(tool *RuntimeToolInfo) (any, bool) {
	if tool == nil || !IsCodexAppsMCPServerName(tool.ServerName) {
		return nil, false
	}
	meta, _ := tool.Meta.(map[string]any)
	appsMeta := connectorMetaMap(meta[MCPToolCodexAppsMetaKey])
	context, ok := appsMeta["invocation_context"]
	if !ok || context == nil {
		return nil, false
	}
	return cloneJSONValue(context), true
}
