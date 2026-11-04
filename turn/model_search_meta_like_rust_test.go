package turn

import (
	"encoding/json"
	"testing"

	"codex_go/mcp"
)

func TestModelSearchRequestMetaIncludesInvocationContextLikeRust(t *testing.T) {
	info := mcp.RuntimeToolInfo{
		ServerName:    mcp.RuntimeCodexAppsMCPServerName,
		HostOwnedApps: true,
		ConnectorID:   mcp.SearchConnector,
		Tool:          mcp.RuntimeTool{Name: "search_service.web_run"},
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

func TestModelSearchRequestMetaIgnoresUnrelatedAppsToolLikeRust(t *testing.T) {
	info := mcp.RuntimeToolInfo{
		ServerName:    mcp.RuntimeCodexAppsMCPServerName,
		HostOwnedApps: true,
		ConnectorID:   "calendar",
		Tool:          mcp.RuntimeTool{Name: "calendar.create_event"},
		Meta:          map[string]any{"_codex_apps": map[string]any{"invocation_context": map[string]any{"unexpected": true}}},
	}
	meta := mcpRuntimeToolRequestMeta(&info)
	apps := meta["_codex_apps"].(map[string]any)
	if _, present := apps["invocation_context"]; present {
		t.Fatalf("unrelated connector received a Search pin: %#v", meta)
	}
	encoded, _ := json.Marshal(meta)
	if string(encoded) != `{"_codex_apps":{"connector_id":"calendar"}}` {
		t.Fatalf("metadata = %s", encoded)
	}
}
