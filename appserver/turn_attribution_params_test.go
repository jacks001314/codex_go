package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/turn"
)

// Mirrors Rust #51402 (`551bd409eb`) `TurnContext::attribution()` /
// `tasks/mod.rs::run_task`: a turn started by a triggered inter-agent
// communication records the sender's agent path as its `initiating_agent_path`,
// and the path is part of the persisted `turn_attribution` (not of the
// Responses request metadata, which Rust never populates with it).
func TestTurnAttributionForParamsCarriesInitiatingAgentPathLikeRust(t *testing.T) {
	params := &turn.TurnStartParams{
		TurnTrigger:         "automation",
		ParentTurnID:        "parent-turn",
		InitiatingAgentPath: "/root/requester",
	}
	attribution := turnAttributionForParams(params, "turn-1", "root-turn")
	if attribution == nil {
		t.Fatalf("attribution is nil")
	}
	if attribution.TurnID != "turn-1" || attribution.TurnTriggerValue() != "automation" || attribution.ParentTurnIDValue() != "parent-turn" {
		t.Fatalf("attribution identity = %#v", attribution)
	}
	if attribution.InitiatingAgentPathValue() != "/root/requester" {
		t.Fatalf("initiating agent path = %#v, want /root/requester", attribution.InitiatingAgentPath)
	}
	if attribution.RootTurnIDValue() != "root-turn" {
		t.Fatalf("root turn = %#v, want root-turn", attribution.RootTurnID)
	}

	// The path stays absent when the turn was not triggered by a delegation.
	plain := turnAttributionForParams(&turn.TurnStartParams{}, "turn-2", "turn-2")
	payload, err := json.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal attribution: %v", err)
	}
	if string(payload) != `{"turn_id":"turn-2","root_turn_id":"turn-2"}` {
		t.Fatalf("serialized attribution = %s", payload)
	}
}

// The initiating agent path is harness-owned: it is never a client-serialized
// turn/start field (Rust's app-server params do not expose it).
func TestTurnStartParamsKeepInitiatingAgentPathInternalLikeRust(t *testing.T) {
	params := &turn.TurnStartParams{ThreadID: "thread-1", InitiatingAgentPath: "/root/requester"}
	payload, err := params.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if _, present := decoded["initiatingAgentPath"]; present {
		t.Fatalf("initiatingAgentPath leaked into the client payload: %s", payload)
	}
}
