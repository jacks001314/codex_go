package session

import (
	"encoding/json"
	"testing"
)

func TestWorldStateRoundTripTracksRuntimeInstructionDomains(t *testing.T) {
	raw, err := EncodeWorldState(&WorldState{
		Model:                  json.RawMessage(`"gpt-5"`),
		Personality:            json.RawMessage(`{"model":"gpt-5","personality":"pragmatic"}`),
		ContextWindowGuidance:  json.RawMessage(`"keep durable notes"`),
		CollaborationMode:      json.RawMessage(`{"mode":"plan"}`),
		PermissionInstructions: json.RawMessage(`{"profile":"workspace-write"}`),
		RealtimeConversation:   json.RawMessage(`{"active":true}`),
		Tools:                  json.RawMessage(`{"mcp__drive":"Drive tools"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := DecodeWorldState(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(state.Model) != `"gpt-5"` || string(state.Personality) != `{"model":"gpt-5","personality":"pragmatic"}` || string(state.ContextWindowGuidance) != `"keep durable notes"` || string(state.CollaborationMode) != `{"mode":"plan"}` || string(state.PermissionInstructions) != `{"profile":"workspace-write"}` || string(state.RealtimeConversation) != `{"active":true}` || string(state.Tools) != `{"mcp__drive":"Drive tools"}` {
		t.Fatalf("state = %#v", state)
	}
}
