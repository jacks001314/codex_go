package telemetry

import (
	"encoding/json"
	"testing"
)

func TestCodexGoalEventSerializesExpectedRustShape(t *testing.T) {
	event := NewCodexGoalEvent(CodexGoalEventInput{
		ThreadID:        "thread-1",
		SessionID:       "session-thread-1",
		TurnID:          stringPtrTelemetry("turn-1"),
		AppServerClient: sampleAppServerClientMetadata(),
		Runtime:         sampleRuntimeMetadata(),
		ThreadSource:    stringPtrTelemetry("user"),
		SubagentSource:  stringPtrTelemetry("thread_spawn"),
		ParentThreadID:  stringPtrTelemetry("parent-thread-1"),
		GoalID:          "goal-1",
		EventKind:       GoalEventKindUsageAccounted,
		GoalStatus:      "budget_limited",
		HasTokenBudget:  true,
		CumulativeTokensAccounted: func() *int64 {
			value := int64(200)
			return &value
		}(),
		CumulativeTimeAccountedSeconds: func() *int64 {
			value := int64(12)
			return &value
		}(),
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
	if payload["event_type"] != CodexGoalEventType ||
		params["thread_id"] != "thread-1" ||
		params["session_id"] != "session-thread-1" ||
		params["turn_id"] != "turn-1" ||
		params["goal_id"] != "goal-1" ||
		params["event_kind"] != GoalEventKindUsageAccounted ||
		params["goal_status"] != "budget_limited" ||
		params["has_token_budget"] != true {
		t.Fatalf("payload = %s", data)
	}
	if params["cumulative_tokens_accounted"] != float64(200) ||
		params["cumulative_time_accounted_seconds"] != float64(12) {
		t.Fatalf("accounting fields = %s", data)
	}
}

func TestCodexGoalEventSerializesNullOptionalsLikeRust(t *testing.T) {
	event := NewCodexGoalEvent(CodexGoalEventInput{
		ThreadID:        "thread-1",
		SessionID:       "session-thread-1",
		AppServerClient: sampleAppServerClientMetadata(),
		Runtime:         sampleRuntimeMetadata(),
		GoalID:          "goal-1",
		EventKind:       GoalEventKindCleared,
		GoalStatus:      "active",
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
	if params["turn_id"] != nil ||
		params["thread_source"] != nil ||
		params["subagent_source"] != nil ||
		params["parent_thread_id"] != nil ||
		params["cumulative_tokens_accounted"] != nil ||
		params["cumulative_time_accounted_seconds"] != nil {
		t.Fatalf("optional null fields = %s", data)
	}
}
