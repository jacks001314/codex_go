package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/codexapi"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust #43002: an ordinary app-server client's `parent_response_id`
// stays inside the `x-codex-turn-metadata` document and is never promoted to
// the request's top-level client metadata (only Guardian reviewer requests add
// it there).
func TestClientParentResponseIDStaysInTurnMetadataLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	defer router.Close()

	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:   t.TempDir(),
		Model: "gpt-test",
	}))
	if started.Error != nil {
		t.Fatalf("thread/start error = %+v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello",
		ResponsesAPIMetadata: map[string]string{
			"parent_response_id": "client-correlation",
			"fiber_run_id":       "fiber-start-123",
		},
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn/start error = %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if _, ok := request.ClientMetadata["parent_response_id"]; ok {
		t.Fatalf("client parent_response_id was promoted to top-level metadata: %#v", request.ClientMetadata)
	}
	if _, ok := request.ClientMetadata["fiber_run_id"]; ok {
		t.Fatalf("client fiber_run_id was promoted to top-level metadata: %#v", request.ClientMetadata)
	}
	raw := request.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]
	if raw == "" {
		t.Fatalf("turn metadata header missing: %#v", request.ClientMetadata)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		t.Fatalf("turn metadata is not JSON: %v (%s)", err, raw)
	}
	if metadata["parent_response_id"] != "client-correlation" {
		t.Fatalf("turn metadata parent_response_id = %#v, want client-correlation", metadata["parent_response_id"])
	}
	if metadata["fiber_run_id"] != "fiber-start-123" {
		t.Fatalf("turn metadata fiber_run_id = %#v, want fiber-start-123", metadata["fiber_run_id"])
	}
}
