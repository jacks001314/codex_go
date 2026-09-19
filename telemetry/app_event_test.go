package telemetry

import (
	"encoding/json"
	"testing"
)

// Mirrors Rust's codex_app_mentioned / codex_app_used shapes: the app metadata is
// flattened into the params, the used event adds the voice session and the
// elicitation classification, and missing values serialize as null.
func TestCodexAppEventsSerializeExpectedRustShape(t *testing.T) {
	metadata := CodexAppMetadata{
		ConnectorID:     stringPtrTelemetry("connector_calendar"),
		ThreadID:        stringPtrTelemetry("thread-1"),
		TurnID:          stringPtrTelemetry("turn-1"),
		AppName:         stringPtrTelemetry("Calendar"),
		ProductClientID: stringPtrTelemetry("codex_cli_rs"),
		InvokeType:      stringPtrTelemetry(InvocationTypeExplicit),
		ModelSlug:       stringPtrTelemetry("gpt-5"),
	}
	mentionedJSON, err := json.Marshal(NewCodexAppMentionedEvent(metadata))
	if err != nil {
		t.Fatalf("marshal mentioned event error = %v", err)
	}
	var mentioned map[string]any
	if err := json.Unmarshal(mentionedJSON, &mentioned); err != nil {
		t.Fatalf("unmarshal mentioned event error = %v", err)
	}
	if mentioned["event_type"] != CodexAppMentionedEventType {
		t.Fatalf("mentioned event type = %#v", mentioned["event_type"])
	}
	mentionedParams := mentioned["event_params"].(map[string]any)
	for key, want := range map[string]any{
		"connector_id":      "connector_calendar",
		"thread_id":         "thread-1",
		"turn_id":           "turn-1",
		"app_name":          "Calendar",
		"product_client_id": "codex_cli_rs",
		"invoke_type":       InvocationTypeExplicit,
		"model_slug":        "gpt-5",
	} {
		if mentionedParams[key] != want {
			t.Fatalf("mentioned params[%s] = %#v, want %#v", key, mentionedParams[key], want)
		}
	}

	classification := ElicitationTypeAuthOrLink
	usedJSON, err := json.Marshal(NewCodexAppUsedEvent(CodexAppUsedEventParams{
		CodexAppMetadata: metadata,
		ElicitationType:  &classification,
	}))
	if err != nil {
		t.Fatalf("marshal used event error = %v", err)
	}
	var used map[string]any
	if err := json.Unmarshal(usedJSON, &used); err != nil {
		t.Fatalf("unmarshal used event error = %v", err)
	}
	if used["event_type"] != CodexAppUsedEventType {
		t.Fatalf("used event type = %#v", used["event_type"])
	}
	usedParams := used["event_params"].(map[string]any)
	if usedParams["connector_id"] != "connector_calendar" || usedParams["elicitation_type"] != ElicitationTypeAuthOrLink {
		t.Fatalf("used params = %#v", usedParams)
	}
	if voice, present := usedParams["voice_session_id"]; !present || voice != nil {
		t.Fatalf("voice_session_id = %#v", voice)
	}

	// An ordinary call reports null for the connector and the classification.
	plainJSON, err := json.Marshal(NewCodexAppUsedEvent(CodexAppUsedEventParams{CodexAppMetadata: CodexAppMetadata{ThreadID: stringPtrTelemetry("thread-1")}}))
	if err != nil {
		t.Fatalf("marshal plain event error = %v", err)
	}
	var plain map[string]any
	if err := json.Unmarshal(plainJSON, &plain); err != nil {
		t.Fatalf("unmarshal plain event error = %v", err)
	}
	plainParams := plain["event_params"].(map[string]any)
	if plainParams["connector_id"] != nil || plainParams["elicitation_type"] != nil || plainParams["invoke_type"] != nil {
		t.Fatalf("plain app-used params = %#v", plainParams)
	}
}
