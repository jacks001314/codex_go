package telemetry

import (
	"encoding/json"
	"testing"
)

func TestCodexCompactionEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexCompactionEvent(CodexCompactionEventInput{
		ThreadID:                  "thread-1",
		SessionID:                 "session-thread-1",
		TurnID:                    "turn-1",
		AppServerClient:           sampleAppServerClientMetadata(),
		Runtime:                   sampleRuntimeMetadata(),
		ThreadSource:              stringPtrTelemetry("user"),
		Trigger:                   CompactionTriggerAuto,
		Reason:                    CompactionReasonContextLimit,
		Implementation:            CompactionImplementationResponsesCompact,
		Phase:                     CompactionPhaseMidTurn,
		Strategy:                  CompactionStrategyMemento,
		Status:                    CompactionStatusCompleted,
		ActiveContextTokensBefore: 120000,
		ActiveContextTokensAfter:  18000,
		StartedAt:                 100,
		CompletedAt:               106,
		DurationMS:                uint64PtrTelemetry(6543),
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
	if payload["event_type"] != CodexCompactionEventType ||
		params["thread_id"] != "thread-1" ||
		params["session_id"] != "session-thread-1" ||
		params["turn_id"] != "turn-1" ||
		params["trigger"] != CompactionTriggerAuto ||
		params["reason"] != CompactionReasonContextLimit ||
		params["implementation"] != CompactionImplementationResponsesCompact ||
		params["phase"] != CompactionPhaseMidTurn ||
		params["strategy"] != CompactionStrategyMemento ||
		params["status"] != CompactionStatusCompleted {
		t.Fatalf("payload = %s", data)
	}
	if params["codex_error_kind"] != nil ||
		params["codex_error_http_status_code"] != nil ||
		params["retained_image_count"] != nil ||
		params["compaction_summary_tokens"] != nil ||
		params["cached_input_tokens"] != nil {
		t.Fatalf("optional null fields = %s", data)
	}
	if params["active_context_tokens_before"] != float64(120000) ||
		params["active_context_tokens_after"] != float64(18000) ||
		params["duration_ms"] != float64(6543) {
		t.Fatalf("numeric fields = %s", data)
	}
}
