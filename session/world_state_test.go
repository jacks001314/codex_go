package session

import (
	"encoding/json"
	"testing"
)

func TestWorldStateRoundTripTracksRuntimeInstructionDomains(t *testing.T) {
	raw, err := EncodeWorldState(&WorldState{
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
	if string(state.CollaborationMode) != `{"mode":"plan"}` || string(state.PermissionInstructions) != `{"profile":"workspace-write"}` || string(state.RealtimeConversation) != `{"active":true}` || string(state.Tools) != `{"mcp__drive":"Drive tools"}` {
		t.Fatalf("state = %#v", state)
	}
}
