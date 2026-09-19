package appserver

import (
	"context"
	"testing"

	"codex_go/model"
	"codex_go/state"
	"codex_go/turn"
)

// Mirrors Rust #45441: the guardian review's `parent_response_id` is the latest
// response id received in the reviewed turn, kept until a later
// `response.created` replaces it and never inherited by a fresh turn.
func TestRuntimeTurnResponseIDKeepsLatestAcrossRetriesLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const (
		threadID = "thread-parent-response-id"
		turnID   = "turn-parent-response-id"
	)
	if err := router.registerActiveRuntimeTurn(threadID, turnID, nil, 0, &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}
	if got := router.guardianLatestResponseIDForTurn(threadID, turnID); got != "" {
		t.Fatalf("turn without response.created parent response id = %q, want empty", got)
	}
	emitCreated := func(responseID string) {
		router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
			Kind:       model.ResponsesStreamEventCreated,
			ResponseID: responseID,
		}, newResponsesStreamNotificationState(false, turnID))
	}
	emitCreated("resp-attempt-1")
	if got := router.guardianLatestResponseIDForTurn(threadID, turnID); got != "resp-attempt-1" {
		t.Fatalf("parent response id = %q, want %q", got, "resp-attempt-1")
	}
	// A retry that never reaches response.created keeps the last known id.
	if got := router.guardianLatestResponseIDForTurn(threadID, turnID); got != "resp-attempt-1" {
		t.Fatalf("parent response id after retry without response.created = %q, want %q", got, "resp-attempt-1")
	}
	emitCreated("resp-attempt-2")
	if got := router.guardianLatestResponseIDForTurn(threadID, turnID); got != "resp-attempt-2" {
		t.Fatalf("parent response id = %q, want %q", got, "resp-attempt-2")
	}
	// A fresh turn must not inherit the previous turn's id.
	if _, ok := router.threads.ConsumeTurn(threadID, turnID, true); !ok {
		t.Fatal("ConsumeTurn() did not find the active turn")
	}
	const nextTurnID = "turn-parent-response-id-2"
	if err := router.registerActiveRuntimeTurn(threadID, nextTurnID, nil, 0, &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn(next) error = %v", err)
	}
	if got := router.guardianLatestResponseIDForTurn(threadID, nextTurnID); got != "" {
		t.Fatalf("fresh turn inherited parent response id %q", got)
	}
}

// The review request must carry the reviewed turn's latest response id as
// `parent_response_id` client metadata, and omit the key before any
// `response.created` arrives (Rust #45441).
func TestGuardianReviewCarriesTurnParentResponseIDLikeRust(t *testing.T) {
	cases := []struct {
		name        string
		createdID   string
		wantPresent bool
	}{
		{name: "in-flight response", createdID: "resp-in-flight", wantPresent: true},
		{name: "before response.created", createdID: "", wantPresent: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := NewRuntimeRouter(RuntimeServices{})
			const (
				threadID = "thread-guardian-metadata"
				turnID   = "turn-guardian-metadata"
			)
			if err := router.registerActiveRuntimeTurn(threadID, turnID, nil, 0, &turn.TurnStartParams{ThreadID: threadID}); err != nil {
				t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
			}
			if tc.createdID != "" {
				router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
					Kind:       model.ResponsesStreamEventCreated,
					ResponseID: tc.createdID,
				}, newResponsesStreamNotificationState(false, turnID))
			}
			var metadata map[string]string
			reviewer := router.ensureGuardianReviewerWithPrewarm(guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
				metadata = request.ClientMetadata
				return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"reviewed"}`}, nil
			}), false)
			if _, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-1", state.Action{Type: "mcp_tool_call", Server: "apps", ToolName: "calendar"}); err != nil {
				t.Fatalf("Review() error = %v", err)
			}
			parent, present := metadata["parent_response_id"]
			if present != tc.wantPresent {
				t.Fatalf("parent_response_id present = %v, want %v (metadata=%#v)", present, tc.wantPresent, metadata)
			}
			if tc.wantPresent && parent != tc.createdID {
				t.Fatalf("parent_response_id = %q, want %q", parent, tc.createdID)
			}
		})
	}
}
