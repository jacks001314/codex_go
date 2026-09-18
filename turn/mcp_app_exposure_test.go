package turn

import (
	"reflect"
	"testing"

	"codex_go/mcp"
	"codex_go/tool"
)

// TestMCPConnectorOmitToolsFromCombinesWithServerRestrictions mirrors Rust
// #46035: a Codex Apps connector's own omissions join its server's, and the
// resulting exposure follows Rust's apply_mcp_tool_exposure_policy table.
func TestMCPConnectorOmitToolsFromCombinesWithServerRestrictions(t *testing.T) {
	appsValues := map[string]any{
		"apps": map[string]any{
			"calendar": map[string]any{"omit_tools_from": []any{"deferred"}},
			"mail":     map[string]any{"omit_tools_from": []any{"code_mode"}},
		},
	}
	appTool := func(connectorID string) mcp.RuntimeToolInfo {
		return mcp.RuntimeToolInfo{
			ServerName:  mcp.RuntimeCodexAppsMCPServerName,
			ConnectorID: connectorID,
			Tool:        mcp.RuntimeTool{Name: "create_event"},
		}
	}

	// A connector that omits deferred discovery exposes its tools directly (and
	// through Code Mode) even while tool search is enabled.
	omit := mcpOmitToolsFromForTool(nil, appsValues, appTool("calendar"))
	if !reflect.DeepEqual(omit, []string{"deferred"}) {
		t.Fatalf("calendar omit = %#v", omit)
	}
	if exposure := mcpToolExposureForSurfaces(omit, true); exposure != tool.ExposureModelVisible {
		t.Fatalf("calendar exposure = %q, want %q", exposure, tool.ExposureModelVisible)
	}

	// A connector that omits Code Mode keeps deferred discovery.
	mailOmit := mcpOmitToolsFromForTool(nil, appsValues, appTool("mail"))
	if exposure := mcpToolExposureForSurfaces(mailOmit, true); exposure != tool.ExposureDeferredModelOnly {
		t.Fatalf("mail exposure = %q, want %q", exposure, tool.ExposureDeferredModelOnly)
	}

	// A connector with no omissions in the apps config keeps the server default.
	if other := mcpOmitToolsFromForTool(nil, appsValues, appTool("other")); len(other) != 0 {
		t.Fatalf("unconfigured connector omit = %#v", other)
	}

	// Non-apps servers and app tools without a connector id never pick up
	// connector-level omissions.
	plain := mcp.RuntimeToolInfo{ServerName: "plain", ConnectorID: "calendar", Tool: mcp.RuntimeTool{Name: "echo"}}
	if omit := mcpOmitToolsFromForTool(nil, appsValues, plain); len(omit) != 0 {
		t.Fatalf("non-apps omit = %#v", omit)
	}
	unidentified := mcp.RuntimeToolInfo{ServerName: mcp.RuntimeCodexAppsMCPServerName, Tool: mcp.RuntimeTool{Name: "echo"}}
	if omit := mcpOmitToolsFromForTool(nil, appsValues, unidentified); len(omit) != 0 {
		t.Fatalf("connector-less omit = %#v", omit)
	}
}

// TestMCPConnectorOmitToolsFromPreservesServerRestrictions keeps Rust's
// restriction-preserving rule: connector omissions never drop a server-level
// omission, and an explicit empty connector list cannot clear one either.
func TestMCPConnectorOmitToolsFromPreservesServerRestrictions(t *testing.T) {
	// Server-level omissions come from the MCP server config; a nil service
	// stands in for a server with no configured omissions.
	appsValues := map[string]any{
		"apps": map[string]any{"calendar": map[string]any{"omit_tools_from": []any{}}},
	}
	info := mcp.RuntimeToolInfo{
		ServerName:  mcp.RuntimeCodexAppsMCPServerName,
		ConnectorID: "calendar",
		Tool:        mcp.RuntimeTool{Name: "create_event"},
	}
	omit := mcpOmitToolsFromForTool(nil, appsValues, info)
	if len(omit) != 0 {
		t.Fatalf("empty connector omissions should not add surfaces: %#v", omit)
	}
	// With tool search enabled the server default keeps the direct surface
	// hidden (deferred discovery plus Code Mode remain), and an empty connector
	// list cannot re-enable it.
	if exposure := mcpToolExposureForSurfaces(omit, true); exposure != tool.ExposureDiscoverable {
		t.Fatalf("exposure = %q, want %q", exposure, tool.ExposureDiscoverable)
	}

	// Unioning surfaces: a connector omitting Code Mode on top of a server that
	// already omits deferred discovery leaves the direct surface alone (Rust's
	// `DirectModelOnly`) - the server's omission is preserved and the connector's
	// adds to it.
	union := mcpToolExposureForSurfaces([]string{"deferred", "code_mode"}, true)
	if union != tool.ExposureDirectModelOnly {
		t.Fatalf("union exposure = %q, want %q", union, tool.ExposureDirectModelOnly)
	}
}
