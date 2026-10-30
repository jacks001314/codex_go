package model

import (
	"encoding/json"
	"testing"

	"codex_go/codexapi"
)

// Mirrors Rust #43002's HTTP and WebSocket assertions: a client
// `parent_response_id` survives only inside the turn-metadata document; the
// request body's top-level client metadata never carries it.
func TestParentResponseIDStaysInsideTurnMetadataInWireBodiesLikeRust(t *testing.T) {
	turnMetadata := `{"parent_response_id":"client-correlation","fiber_run_id":"fiber-start-123"}`
	apiRequest := &responsesAgentRequest{
		Model: "gpt-5",
		ClientMetadata: map[string]string{
			codexapi.ClientCodexTurnMetadataHeader: turnMetadata,
		},
	}

	body, err := json.Marshal(apiRequest)
	if err != nil {
		t.Fatalf("marshal HTTP body: %v", err)
	}
	var decoded struct {
		ClientMetadata map[string]string `json:"client_metadata"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode HTTP body: %v", err)
	}
	assertParentResponseIDOnlyInTurnMetadata(t, "http body", decoded.ClientMetadata, turnMetadata)

	websocket := websocketResponseCreatePayload(apiRequest, "resp-previous", nil, nil)
	websocketMetadata, ok := websocket["client_metadata"].(map[string]string)
	if !ok {
		t.Fatalf("websocket client_metadata = %#v", websocket["client_metadata"])
	}
	assertParentResponseIDOnlyInTurnMetadata(t, "websocket body", websocketMetadata, turnMetadata)
}

func assertParentResponseIDOnlyInTurnMetadata(t *testing.T, label string, metadata map[string]string, wantTurnMetadata string) {
	t.Helper()
	if _, ok := metadata["parent_response_id"]; ok {
		t.Fatalf("%s promoted parent_response_id: %#v", label, metadata)
	}
	if _, ok := metadata["fiber_run_id"]; ok {
		t.Fatalf("%s promoted fiber_run_id: %#v", label, metadata)
	}
	if got := metadata[codexapi.ClientCodexTurnMetadataHeader]; got != wantTurnMetadata {
		t.Fatalf("%s turn metadata = %q, want %q", label, got, wantTurnMetadata)
	}
}
