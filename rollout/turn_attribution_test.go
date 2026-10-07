package rollout

import (
	"encoding/json"
	"testing"
)

// Mirrors Rust #51402 (`551bd409eb`) `TurnStartedEvent.turn_attribution`: the
// persisted provenance round-trips with the Rust snake_case wire names, and an
// absent optional field stays absent rather than becoming an empty string.
func TestTurnStartedAttributionWireLikeRust(t *testing.T) {
	raw := []byte(`{
		"type": "task_started",
		"turn_id": "turn-1",
		"started_at": 1700000000,
		"turn_attribution": {
			"turn_id": "turn-1",
			"turn_trigger": "user",
			"parent_turn_id": "turn-0",
			"initiating_agent_path": "/root/child",
			"root_turn_id": "turn-0"
		}
	}`)
	var payload rolloutEventPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal turn_started error = %v", err)
	}
	attribution := payload.TurnAttribution
	if !attribution.Valid() {
		t.Fatalf("attribution = %#v, want valid", attribution)
	}
	if attribution.TurnID != "turn-1" || attribution.TurnTriggerValue() != "user" {
		t.Fatalf("attribution identity = %#v", attribution)
	}
	if attribution.ParentTurnID == nil || *attribution.ParentTurnID != "turn-0" {
		t.Fatalf("parent turn = %#v, want turn-0", attribution.ParentTurnID)
	}
	if attribution.InitiatingAgentPath == nil || *attribution.InitiatingAgentPath != "/root/child" {
		t.Fatalf("initiating agent path = %#v, want /root/child", attribution.InitiatingAgentPath)
	}
	if attribution.RootTurnID == nil || *attribution.RootTurnID != "turn-0" {
		t.Fatalf("root turn = %#v, want turn-0", attribution.RootTurnID)
	}
}

// A legacy `turn_started` line without attribution decodes to nil, and the
// trigger stays absent when only the turn id was recorded.
func TestTurnStartedAttributionLegacyAndAbsentFieldsLikeRust(t *testing.T) {
	var legacy rolloutEventPayload
	if err := json.Unmarshal([]byte(`{"type":"task_started","turn_id":"turn-1"}`), &legacy); err != nil {
		t.Fatalf("legacy unmarshal error = %v", err)
	}
	if legacy.TurnAttribution != nil {
		t.Fatalf("legacy attribution = %#v, want nil", legacy.TurnAttribution)
	}

	var partial rolloutEventPayload
	if err := json.Unmarshal([]byte(`{"type":"task_started","turn_id":"turn-2","turn_attribution":{"turn_id":"turn-2"}}`), &partial); err != nil {
		t.Fatalf("partial unmarshal error = %v", err)
	}
	if partial.TurnAttribution == nil || partial.TurnAttribution.TurnTriggerValue() != "" {
		t.Fatalf("partial attribution = %#v, want absent trigger", partial.TurnAttribution)
	}
	if !partial.TurnAttribution.Valid() {
		t.Fatalf("partial attribution should still be valid")
	}
}
