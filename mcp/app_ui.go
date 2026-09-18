package mcp

import "strings"

// Meta keys mirror Rust's mcp_tool_call.rs constants (#45805).
const (
	mcpToolUIMetaKey                    = "ui"
	mcpToolUIResourceURIMetaKey         = "ui/resourceUri"
	mcpToolOpenAIOutputTemplateMetaKey  = "openai/outputTemplate"
	mcpToolOpenAIUIMetaKey              = "openai/ui"
	mcpToolPreferredModelDisplayModeKey = "preferredModelDisplayMode"
)

// McpAppDisplayMode mirrors Rust's `McpAppDisplayMode`: how the client should
// present an MCP app widget for this invocation.
type McpAppDisplayMode string

const (
	// McpAppDisplayModeInline is the default when the descriptor declares no
	// preference or one the client does not support.
	McpAppDisplayModeInline McpAppDisplayMode = "inline"
	// McpAppDisplayModeFullscreen requests the fullscreen presentation.
	McpAppDisplayModeFullscreen McpAppDisplayMode = "fullscreen"
)

// McpAppUI mirrors Rust's `McpAppUi`: the UI resource captured from the invoked
// descriptor plus its preferred model display mode.
type McpAppUI struct {
	ResourceURI               string            `json:"resourceUri"`
	PreferredModelDisplayMode McpAppDisplayMode `json:"preferredModelDisplayMode"`
}

// MCPAppUIFromToolMeta mirrors Rust's `get_mcp_app_resource_uri` plus the
// `mcp_tool_metadata` display-mode mapping (#45805): the resource URI comes from
// `ui.resourceUri`, then `ui/resourceUri`, then `openai/outputTemplate`, and the
// display mode is `fullscreen` only when `openai/ui.preferredModelDisplayMode`
// says so - anything else (including an unsupported mode) is `inline`. Nil means
// the tool declares no widget, so clients fall back to catalog discovery.
func MCPAppUIFromToolMeta(meta any) *McpAppUI {
	resourceURI := mcpToolUIResourceURI(meta)
	if resourceURI == "" {
		return nil
	}
	return &McpAppUI{
		ResourceURI:               resourceURI,
		PreferredModelDisplayMode: mcpToolPreferredModelDisplayMode(meta),
	}
}

func mcpToolUIResourceURI(meta any) string {
	base := metadataMap(meta)
	if base == nil {
		return ""
	}
	if ui := metadataMap(base[mcpToolUIMetaKey]); ui != nil {
		if value := stringFromMetadata(ui["resourceUri"]); value != "" {
			return value
		}
	}
	if value := stringFromMetadata(base[mcpToolUIResourceURIMetaKey]); value != "" {
		return value
	}
	return stringFromMetadata(base[mcpToolOpenAIOutputTemplateMetaKey])
}

func mcpToolPreferredModelDisplayMode(meta any) McpAppDisplayMode {
	base := metadataMap(meta)
	if base == nil {
		return McpAppDisplayModeInline
	}
	ui := metadataMap(base[mcpToolOpenAIUIMetaKey])
	if ui == nil {
		return McpAppDisplayModeInline
	}
	if stringFromMetadata(ui[mcpToolPreferredModelDisplayModeKey]) == string(McpAppDisplayModeFullscreen) {
		return McpAppDisplayModeFullscreen
	}
	return McpAppDisplayModeInline
}

// MCPToolCallLinkID returns the trusted `_codex_apps` link id for a Codex Apps
// tool call, when the descriptor carries one (Rust's MCP_TOOL_LINK_ID_META_KEY).
func MCPToolCallLinkID(meta any) string {
	base := metadataMap(meta)
	if base == nil {
		return ""
	}
	for _, key := range []string{"_codex_apps", "codex_apps", "codexApps"} {
		if nested := metadataMap(base[key]); nested != nil {
			if value := stringFromMetadata(nested["link_id"]); value != "" {
				return value
			}
		}
	}
	return ""
}

func stringFromMetadata(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

// MCPAppUIFromMetadataMap decodes a captured widget presentation map (item data
// or rollout JSON) back into the typed form, reporting nil when it carries no
// resource URI.
func MCPAppUIFromMetadataMap(value map[string]any) *McpAppUI {
	if value == nil {
		return nil
	}
	resourceURI := ""
	for _, key := range []string{"resourceUri", "resource_uri"} {
		if text := stringFromMetadata(value[key]); text != "" {
			resourceURI = text
			break
		}
	}
	if resourceURI == "" {
		return nil
	}
	mode := McpAppDisplayModeInline
	for _, key := range []string{"preferredModelDisplayMode", "preferred_model_display_mode"} {
		if stringFromMetadata(value[key]) == string(McpAppDisplayModeFullscreen) {
			mode = McpAppDisplayModeFullscreen
			break
		}
	}
	return &McpAppUI{ResourceURI: resourceURI, PreferredModelDisplayMode: mode}
}

// ApplyMCPAppOutputMetadata records the MCP app presentation and the trusted
// Codex Apps identity captured for one call on the tool output's data map
// (Rust #45805 plus `McpToolCallItemMetadata::from_tool_metadata`): the widget
// presentation comes from the invoked descriptor, while the connector, link,
// app, and action identity may only be contributed by the codex_apps server.
func ApplyMCPAppOutputMetadata(data map[string]any, server string, toolMeta any, connectorID string, connectorName string, pluginID string) {
	if data == nil {
		return
	}
	if appUI := MCPAppUIFromToolMeta(toolMeta); appUI != nil {
		data["mcp_app_ui"] = *appUI
	}
	// Rust carries the owning plugin id for every server, not only the trusted
	// apps server.
	if value := strings.TrimSpace(pluginID); value != "" {
		data["plugin_id"] = value
	}
	if !IsCodexAppsMCPServerName(strings.TrimSpace(server)) {
		return
	}
	if value := strings.TrimSpace(connectorID); value != "" {
		data["connector_id"] = value
	}
	if value := strings.TrimSpace(connectorName); value != "" {
		data["connector_name"] = value
	}
	if value := MCPToolCallLinkID(toolMeta); value != "" {
		data["link_id"] = value
	}
	if value := mcpToolCallActionName(toolMeta); value != "" {
		data["action_name"] = value
	}
}
