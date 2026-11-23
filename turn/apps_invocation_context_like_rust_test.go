package turn

import (
	"encoding/json"
	"testing"

	"codex_go/mcp"
)

// Rust #62808 removed the model-selected Search MCP projection, so the Search
// connector is an ordinary Codex Apps tool. Rust still forwards the opaque
// connector-owned `_codex_apps.invocation_context` member on every Apps call
// (mcp_tool_call.rs apps_invocation_context); the pin is cloned so a caller
// cannot rewrite the registration's value.
func TestAppsRequestMetaIncludesInvocationContextLikeRust(t *testing.T) {
	info := mcp.RuntimeToolInfo{
		ServerName:  mcp.RuntimeCodexAppsMCPServerName,
		ConnectorID: "calendar",
		Tool:        mcp.RuntimeTool{Name: "calendar.create_event"},
		Meta: map[string]any{
			"_codex_apps": map[string]any{
				"invocation_context": map[string]any{"config": map[string]any{"tool_type": "SonicBrowserToolV99"}},
			},
		},
	}
	meta := mcpRuntimeToolRequestMeta(&info)
	apps, _ := meta["_codex_apps"].(map[string]any)
	pin, _ := apps["invocation_context"].(map[string]any)
	configValue, _ := pin["config"].(map[string]any)
	if configValue["tool_type"] != "SonicBrowserToolV99" {
		t.Fatalf("request metadata = %#v", meta)
	}
	pin["config"].(map[string]any)["tool_type"] = "changed"
	metaAgain := mcpRuntimeToolRequestMeta(&info)
	if metaAgain["_codex_apps"].(map[string]any)["invocation_context"].(map[string]any)["config"].(map[string]any)["tool_type"] != "SonicBrowserToolV99" {
		t.Fatal("invocation context was not cloned")
	}
}

func TestAppsRequestMetaLeavesOtherServersUntouchedLikeRust(t *testing.T) {
	info := mcp.RuntimeToolInfo{
		ServerName: "third_party",
		Tool:       mcp.RuntimeTool{Name: "third_party.run"},
		Meta:       map[string]any{"_codex_apps": map[string]any{"invocation_context": map[string]any{"unexpected": true}}},
	}
	if meta := mcpRuntimeToolRequestMeta(&info); meta != nil {
		t.Fatalf("non-Apps request metadata = %#v, want nil", meta)
	}
}

func TestAppsRequestMetaOmitsAbsentInvocationContextLikeRust(t *testing.T) {
	info := mcp.RuntimeToolInfo{
		ServerName:  mcp.RuntimeCodexAppsMCPServerName,
		ConnectorID: "calendar",
		Tool:        mcp.RuntimeTool{Name: "calendar.create_event"},
	}
	meta := mcpRuntimeToolRequestMeta(&info)
	encoded, _ := json.Marshal(meta)
	if string(encoded) != `{"_codex_apps":{"connector_id":"calendar"}}` {
		t.Fatalf("metadata = %s", encoded)
	}
}
