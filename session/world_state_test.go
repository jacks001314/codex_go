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

func TestWorldStatePersistedRepresentationIsObjectLikeRust(t *testing.T) {
	raw, err := EncodeWorldState(&WorldState{Model: json.RawMessage(`"gpt-5"`)})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("encoded world state is not an object: %v", err)
	}
	if len(object) != 1 || object["model"] != "gpt-5" {
		t.Fatalf("object = %#v", object)
	}
	if _, err := DecodeWorldState(json.RawMessage(`[]`)); err == nil {
		t.Fatal("DecodeWorldState accepted a non-object JSON array")
	}
}
