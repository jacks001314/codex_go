package telemetry

import (
	"encoding/json"
	"testing"
)

func TestCodexHookRunEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexHookRunEvent(CodexHookRunMetadataV1{
		ThreadID:        stringPtrTelemetry("thread-3"),
		TurnID:          stringPtrTelemetry("turn-3"),
		ProductClientID: stringPtrTelemetry("codex_cli_rs"),
		ModelSlug:       stringPtrTelemetry("gpt-5"),
		HookName:        stringPtrTelemetry("PreToolUse"),
		HookSource:      stringPtrTelemetry("user"),
		Status:          stringPtrTelemetry("completed"),
	})

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	params := payload["event_params"].(map[string]any)
	if payload["event_type"] != CodexHookRunEventType ||
		params["thread_id"] != "thread-3" ||
		params["turn_id"] != "turn-3" ||
		params["product_client_id"] != "codex_cli_rs" ||
		params["model_slug"] != "gpt-5" ||
		params["hook_name"] != "PreToolUse" ||
		params["hook_source"] != "user" ||
		params["status"] != "completed" {
		t.Fatalf("payload = %s", data)
	}
}
