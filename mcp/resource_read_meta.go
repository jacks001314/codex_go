package mcp

import "strings"

// mcpResourceReadRequestParams builds resources/read params with the `_meta`
// entries Rust forwards for scoped and direct hosted app reads (#39244,
// #47248). A direct target is omitted when originCallId scope is present.
func mcpResourceReadRequestParams(params *MCPResourceReadParams) map[string]any {
	payload := map[string]any{"uri": ""}
	if params != nil {
		payload["uri"] = strings.TrimSpace(params.URI)
	}
	if params == nil {
		return payload
	}
	meta := map[string]any{}
	if connector := strings.TrimSpace(params.ConnectorID); connector != "" {
		meta[MCPToolTurnMetadataMetaKey] = map[string]any{
			"mcp_request_meta": map[string]any{
				"selected_connector_ids": []string{connector},
			},
		}
	}
	if params.Target != nil && strings.TrimSpace(params.OriginCallID) == "" {
		meta["connector_id"] = strings.TrimSpace(params.Target.ConnectorID)
		if params.Target.LinkID != nil {
			meta["link_id"] = strings.TrimSpace(*params.Target.LinkID)
		} else {
			meta["link_id"] = nil
		}
	}
	if len(meta) > 0 {
		payload["_meta"] = meta
	}
	return payload
}
