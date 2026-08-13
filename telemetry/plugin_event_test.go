package telemetry

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestCodexPluginEventSerializesExpectedRustShape(t *testing.T) {
	metadata := CodexPluginMetadata{
		PluginID:        stringPtrTelemetry("sample@test"),
		RemotePluginID:  nil,
		PluginName:      stringPtrTelemetry("sample"),
		MarketplaceName: stringPtrTelemetry("test"),
		HasSkills:       boolPtrTelemetry(true),
		MCPServerCount:  intPtrTelemetry(2),
		ConnectorIDs:    []string{"calendar", "drive"},
		ProductClientID: stringPtrTelemetry("codex_cli_rs"),
	}
	event := NewCodexPluginEvent(CodexPluginInstalledEventType, metadata)

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	params := payload["event_params"].(map[string]any)
	if payload["event_type"] != CodexPluginInstalledEventType ||
		params["plugin_id"] != "sample@test" ||
		params["remote_plugin_id"] != nil ||
		params["plugin_name"] != "sample" ||
		params["marketplace_name"] != "test" ||
		params["has_skills"] != true ||
		params["mcp_server_count"] != float64(2) ||
		params["product_client_id"] != "codex_cli_rs" {
		t.Fatalf("payload = %s", data)
	}
	if !reflect.DeepEqual(params["connector_ids"], []any{"calendar", "drive"}) {
		t.Fatalf("connector_ids = %#v", params["connector_ids"])
	}
}

func TestPluginMeasurementEventsBoundAndValidateLikeRust(t *testing.T) {
	input := CodexPluginMeasurementsInput{
		ThreadID:    "thread-1",
		TurnID:      "turn-1",
		ItemID:      "item-1",
		PluginID:    "sample@test",
		ExecutionID: "exec-1",
		Operation:   "run_measure",
		Rows: []PluginMeasurementRow{
			{MeasurementName: "tokens", NumberValue: 12.5, Dimensions: map[string]string{"speed": "fast"}},
			{MeasurementName: "bad-name", NumberValue: 1, Dimensions: nil},
			{MeasurementName: "tokens", NumberValue: math.NaN(), Dimensions: nil},
			{MeasurementName: "tokens", NumberValue: 3, Dimensions: map[string]string{"speed": "invalid value"}},
		},
	}
	events := PluginMeasurementEvents(input)
	if len(events) != 1 || events[0].EventType != CodexPluginMeasurementEventType {
		t.Fatalf("events = %#v", events)
	}
	if events[0].EventParams.MeasurementName != "tokens" || events[0].EventParams.Dimensions["speed"] != "fast" {
		t.Fatalf("event params = %#v", events[0].EventParams)
	}
}

func TestCodexPluginInstallFailedEventSerializesExpectedRustShape(t *testing.T) {
	metadata := CodexPluginMetadata{
		PluginID:        stringPtrTelemetry("sample@test"),
		PluginName:      stringPtrTelemetry("sample"),
		MarketplaceName: stringPtrTelemetry("test"),
		ProductClientID: stringPtrTelemetry("codex_cli_rs"),
	}
	event := NewCodexPluginInstallFailedEvent(metadata, "store_invalid")

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	params := payload["event_params"].(map[string]any)
	if payload["event_type"] != CodexPluginInstallFailedEventType ||
		params["plugin_id"] != "sample@test" ||
		params["remote_plugin_id"] != nil ||
		params["plugin_name"] != "sample" ||
		params["marketplace_name"] != "test" ||
		params["has_skills"] != nil ||
		params["mcp_server_count"] != nil ||
		params["connector_ids"] != nil ||
		params["product_client_id"] != "codex_cli_rs" ||
		params["error_type"] != "store_invalid" {
		t.Fatalf("payload = %s", data)
	}
}
