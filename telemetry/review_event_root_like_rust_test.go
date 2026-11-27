package telemetry

import (
	"encoding/json"
	"testing"
)

func TestGuardianReviewEventCarriesActionRootLikeRust(t *testing.T) {
	root := "issuing-root"
	event := NewGuardianReviewEvent(GuardianReviewEventParams{
		ThreadID:              "thread",
		TurnID:                "servicing-turn",
		RootTurnID:            &root,
		ReviewID:              "review",
		ApprovalRequestSource: "main_turn",
		ReviewedAction:        json.RawMessage(`{"type":"native_tool_call"}`),
		Decision:              "approved",
		TerminalStatus:        "approved",
		AttemptCount:          1,
	})
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got struct {
		EventType   string         `json:"event_type"`
		EventParams map[string]any `json:"event_params"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.EventType != CodexGuardianReviewEventType {
		t.Fatalf("event_type = %q, want %q", got.EventType, CodexGuardianReviewEventType)
	}
	if got.EventParams["root_turn_id"] != root {
		t.Fatalf("root_turn_id = %#v, want %q", got.EventParams["root_turn_id"], root)
	}

	event.EventParams.RootTurnID = nil
	data, err = json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal(unknown root) error = %v", err)
	}
	got.EventParams = nil
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(unknown root) error = %v", err)
	}
	value, present := got.EventParams["root_turn_id"]
	if !present || value != nil {
		t.Fatalf("unknown root = %#v (present %v), want null", value, present)
	}
}

func TestCodexReviewEventDoesNotCarryGuardianRootLikeRust(t *testing.T) {
	data, err := json.Marshal(NewCodexReviewEvent(CodexReviewEventParams{ThreadID: "thread", TurnID: "turn"}))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got struct {
		EventParams map[string]any `json:"event_params"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, present := got.EventParams["root_turn_id"]; present {
		t.Fatalf("codex_review_event unexpectedly carries root_turn_id: %#v", got.EventParams)
	}
}
