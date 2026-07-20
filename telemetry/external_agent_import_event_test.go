package telemetry

import (
	"encoding/json"
	"testing"
)

func TestCodexOnboardingExternalAgentImportCompleteEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexOnboardingExternalAgentImportCompleteEvent(CodexOnboardingExternalAgentImportCompleteMetadata{
		ImportID:        "import-1",
		Source:          "app_server",
		ItemType:        "PLUGINS",
		SuccessCount:    2,
		FailedCount:     1,
		ProductClientID: stringPtrTelemetry("codex_cli_rs"),
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
	if payload["event_type"] != CodexOnboardingExternalAgentImportCompleteEventType ||
		params["import_id"] != "import-1" ||
		params["source"] != "app_server" ||
		params["type"] != "PLUGINS" ||
		params["success_count"] != float64(2) ||
		params["failed_count"] != float64(1) ||
		params["product_client_id"] != "codex_cli_rs" {
		t.Fatalf("payload = %s", data)
	}
}

func TestCodexOnboardingExternalAgentImportFailureEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexOnboardingExternalAgentImportFailureEvent(CodexOnboardingExternalAgentImportFailureMetadata{
		ImportID:        "import-1",
		Source:          "app_server",
		ItemType:        "SESSIONS",
		FailureStage:    "session_missing",
		ErrorType:       "session_missing",
		SubErrorType:    stringPtrTelemetry("session_not_detected"),
		ProductClientID: stringPtrTelemetry("codex_cli_rs"),
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
	if payload["event_type"] != CodexOnboardingExternalAgentImportFailureEventType ||
		params["import_id"] != "import-1" ||
		params["source"] != "app_server" ||
		params["type"] != "SESSIONS" ||
		params["failure_stage"] != "session_missing" ||
		params["error_type"] != "session_missing" ||
		params["sub_error_type"] != "session_not_detected" ||
		params["product_client_id"] != "codex_cli_rs" {
		t.Fatalf("payload = %s", data)
	}
}
