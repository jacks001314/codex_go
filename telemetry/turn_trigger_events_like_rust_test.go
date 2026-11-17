package telemetry

import (
	"encoding/json"
	"testing"
)

func turnTriggerEventParams(t *testing.T, event any) map[string]any {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal %T error = %v", event, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal %T error = %v", event, err)
	}
	params, ok := payload["event_params"].(map[string]any)
	if !ok {
		t.Fatalf("%T event_params = %#v", event, payload["event_params"])
	}
	return params
}

// Rust analytics/src/events.rs (#60018): every usage event family reports the
// turn trigger it belongs to, and an absent trigger stays off the wire.
func TestUsageEventParamsCarryTurnTriggerLikeRust(t *testing.T) {
	trigger := "automation_cron_scheduled"
	base := CodexToolItemEventBase{TurnTrigger: &trigger, ThreadID: "thread-1", TurnID: "turn-1"}
	events := []any{
		NewCodexCommandExecutionEvent(CodexCommandExecutionEventParams{CodexToolItemEventBase: base}),
		NewCodexFileChangeEvent(CodexFileChangeEventParams{CodexToolItemEventBase: base}),
		NewCodexMCPToolCallEvent(CodexMCPToolCallEventParams{CodexToolItemEventBase: base}),
		NewCodexDynamicToolCallEvent(CodexDynamicToolCallEventParams{CodexToolItemEventBase: base}),
		NewCodexWebSearchEvent(CodexWebSearchEventParams{CodexToolItemEventBase: base}),
		NewCodexImageGenerationEvent(CodexImageGenerationEventParams{CodexToolItemEventBase: base}),
		NewArtifactOperationEvent(ArtifactOperationEventParams{TurnTrigger: &trigger, ThreadID: "thread-1", TurnID: "turn-1"}),
		NewCodexHookRunEvent(CodexHookRunMetadataV1{TurnTrigger: &trigger}),
		NewCodexAppUsedEvent(CodexAppUsedEventParams{CodexAppMetadata: CodexAppMetadata{TurnTrigger: &trigger}}),
		NewCodexAppMentionedEvent(CodexAppMetadata{TurnTrigger: &trigger}),
		SkillInvocationEventRequest{
			EventType:   SkillInvocationEventType,
			SkillID:     "skill-1",
			SkillName:   "skill",
			EventParams: SkillInvocationEventParams{TurnTrigger: &trigger},
		},
	}
	for _, event := range events {
		if got := turnTriggerEventParams(t, event)["turn_trigger"]; got != trigger {
			t.Fatalf("%T turn_trigger = %#v, want %q", event, got, trigger)
		}
	}
}

// The trigger is absent (not null) when the turn carried none, matching Rust's
// `#[serde(skip_serializing_if = "Option::is_none")]`.
func TestUsageEventParamsOmitAbsentTurnTriggerLikeRust(t *testing.T) {
	base := CodexToolItemEventBase{ThreadID: "thread-1", TurnID: "turn-1"}
	events := []any{
		NewCodexCommandExecutionEvent(CodexCommandExecutionEventParams{CodexToolItemEventBase: base}),
		NewArtifactOperationEvent(ArtifactOperationEventParams{ThreadID: "thread-1", TurnID: "turn-1"}),
		NewCodexHookRunEvent(CodexHookRunMetadataV1{}),
		NewCodexAppUsedEvent(CodexAppUsedEventParams{}),
		SkillInvocationEventRequest{EventParams: SkillInvocationEventParams{}},
	}
	for _, event := range events {
		if _, present := turnTriggerEventParams(t, event)["turn_trigger"]; present {
			t.Fatalf("%T must omit an absent turn_trigger", event)
		}
	}
}
