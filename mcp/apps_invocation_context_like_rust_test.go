package mcp

import "testing"

// Rust #62808 removed the Search-only discovery and projection, so a Codex
// Apps declaration that names the Search connector is projected like any other
// Apps tool; its connector-owned invocation context still reaches the call.
func TestAppsInvocationContextCoversSearchConnectorLikeRust(t *testing.T) {
	info := RuntimeToolInfo{
		ServerName:  RuntimeCodexAppsMCPServerName,
		ConnectorID: "connector_openai_search_service",
		Tool:        RuntimeTool{Name: "search_service.web_run"},
		Meta: map[string]any{
			MCPToolCodexAppsMetaKey: map[string]any{
				"invocation_context": map[string]any{"config": map[string]any{"tool_type": "SonicBrowserToolV99"}},
			},
		},
	}
	context, ok := AppsInvocationContext(&info)
	if !ok {
		t.Fatal("AppsInvocationContext() = missing")
	}
	pin, _ := context.(map[string]any)
	configValue, _ := pin["config"].(map[string]any)
	if configValue["tool_type"] != "SonicBrowserToolV99" {
		t.Fatalf("invocation context = %#v", context)
	}
	pin["config"].(map[string]any)["tool_type"] = "changed"
	again, _ := AppsInvocationContext(&info)
	if again.(map[string]any)["config"].(map[string]any)["tool_type"] != "SonicBrowserToolV99" {
		t.Fatal("invocation context was not cloned")
	}
}

func TestAppsInvocationContextRequiresAppsRegistrationLikeRust(t *testing.T) {
	info := RuntimeToolInfo{
		ServerName: "third_party",
		Meta:       map[string]any{MCPToolCodexAppsMetaKey: map[string]any{"invocation_context": map[string]any{"unexpected": true}}},
	}
	if _, ok := AppsInvocationContext(&info); ok {
		t.Fatal("non-Apps registration exposed an invocation context")
	}
	if _, ok := AppsInvocationContext(nil); ok {
		t.Fatal("nil tool exposed an invocation context")
	}
}
