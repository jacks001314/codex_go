package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust analytics/src/tests/suite/plugins_tests.rs::
// plugin_used_event_serializes_expected_shape: the plugin identity is flattened
// into the params, followed by mcp_server_names, thread_id, turn_id and
// model_slug.
func TestCodexPluginUsedEventSerializesExpectedRustShape(t *testing.T) {
	metadata := CodexPluginUsedMetadata{
		CodexPluginMetadata: CodexPluginMetadata{
			PluginID:        pluginUsedTestStringPtr("sample@test"),
			PluginName:      pluginUsedTestStringPtr("sample"),
			MarketplaceName: pluginUsedTestStringPtr("test"),
			HasSkills:       pluginUsedTestBoolPtr(true),
			MCPServerCount:  pluginUsedTestIntPtr(2),
			ConnectorIDs:    []string{"calendar", "drive"},
			ProductClientID: pluginUsedTestStringPtr("codex_cli_go"),
		},
		MCPServerNames: []string{"mcp-1", "mcp-2"},
		ThreadID:       pluginUsedTestStringPtr("thread-3"),
		TurnID:         pluginUsedTestStringPtr("turn-3"),
		ModelSlug:      pluginUsedTestStringPtr("gpt-5"),
	}

	payload, err := json.Marshal(NewCodexPluginUsedEvent(metadata))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	want := `{"event_type":"codex_plugin_used","event_params":{"plugin_id":"sample@test","remote_plugin_id":null,"plugin_name":"sample","marketplace_name":"test","has_skills":true,"mcp_server_count":2,"connector_ids":["calendar","drive"],"product_client_id":"codex_cli_go","mcp_server_names":["mcp-1","mcp-2"],"thread_id":"thread-3","turn_id":"turn-3","model_slug":"gpt-5"}}`
	if string(payload) != want {
		t.Fatalf("payload = %s\nwant %s", payload, want)
	}
}

// Rust keeps turn_trigger first and omits it when the turn metadata carries no
// trigger (`#[serde(skip_serializing_if = "Option::is_none")]`).
func TestCodexPluginUsedEventCarriesAndOmitsTurnTriggerLikeRust(t *testing.T) {
	metadata := CodexPluginUsedMetadata{
		CodexPluginMetadata: CodexPluginMetadata{
			PluginID:     pluginUsedTestStringPtr("sample@test"),
			ConnectorIDs: []string{},
		},
		MCPServerNames: []string{},
		ThreadID:       pluginUsedTestStringPtr("thread-3"),
		TurnID:         pluginUsedTestStringPtr("turn-3"),
	}

	absent, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(absent[:1]) != "{" || containsJSONKey(t, absent, "turn_trigger") {
		t.Fatalf("absent-trigger payload = %s", absent)
	}

	metadata.TurnTrigger = pluginUsedTestStringPtr("user_message")
	carried, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(carried, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded["turn_trigger"] != "user_message" {
		t.Fatalf("turn_trigger = %#v", decoded["turn_trigger"])
	}
	if !strings.HasPrefix(string(carried), `{"turn_trigger":"user_message"`) {
		t.Fatalf("turn_trigger does not lead the params: %s", carried)
	}
}

func containsJSONKey(t *testing.T, payload []byte, key string) bool {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	_, ok := decoded[key]
	return ok
}

func pluginUsedTestStringPtr(value string) *string { return &value }
func pluginUsedTestBoolPtr(value bool) *bool       { return &value }
func pluginUsedTestIntPtr(value int) *int          { return &value }
