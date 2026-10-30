package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/network"
	"codex_go/state"
	"codex_go/turn"
)

func guardianTestRoutingAffinity(t *testing.T, value string) *network.RoutingAffinity {
	t.Helper()
	header := http.Header{}
	header.Set("Set-Cookie", "__oailb="+value+"; Path=/; Max-Age=3600; Secure")
	affinity := network.RoutingAffinityFromResponse("wss://edge.chatgpt.com/backend-api/codex/responses", header)
	if affinity == nil {
		t.Fatalf("failed to capture routing pin %q", value)
	}
	return affinity
}

// Mirrors Rust #57710: the review request carries the routing pin captured
// with the parent response, and the pin never enters the prompt or metadata.
func TestGuardianReviewCarriesParentRoutingAffinityLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const threadID, turnID = "thread-guardian-affinity", "turn-guardian-affinity"
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}
	affinity := guardianTestRoutingAffinity(t, "parent-edge")
	router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
		Kind:            model.ResponsesStreamEventCreated,
		ResponseID:      "resp-parent-affinity",
		RoutingAffinity: affinity,
	}, newResponsesStreamNotificationState(false, turnID))
	if id, got := router.guardianLatestParentResponseForTurn(threadID, turnID); id != "resp-parent-affinity" || got != affinity {
		t.Fatalf("parent response = %q, %v; want the created response and its pin", id, got)
	}

	var captured *model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			captured = request
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:          state.NewReviewStore(),
		parentResponse: router.guardianLatestParentResponseForTurn,
	}
	if _, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-1", state.Action{Type: "command", Command: "ls", CWD: "/tmp"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if captured == nil {
		t.Fatal("reviewer did not sample")
	}
	if captured.RoutingAffinity != affinity {
		t.Fatalf("review routing affinity = %v, want the parent pin", captured.RoutingAffinity)
	}
	if got := captured.ClientMetadata["parent_response_id"]; got != "resp-parent-affinity" {
		t.Fatalf("parent_response_id = %q", got)
	}
	metadata, err := json.Marshal(captured.ClientMetadata)
	if err != nil {
		t.Fatalf("marshal client metadata: %v", err)
	}
	if strings.Contains(string(metadata), "parent-edge") || strings.Contains(captured.Prompt, "parent-edge") {
		t.Fatalf("routing cookie leaked into metadata=%s or prompt", metadata)
	}

	// A newer parent response without a pin drops both the pin and the mismatch.
	router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
		Kind:       model.ResponsesStreamEventCreated,
		ResponseID: "resp-parent-http",
	}, newResponsesStreamNotificationState(false, turnID))
	if _, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-2", state.Action{Type: "command", Command: "ls", CWD: "/tmp"}); err != nil {
		t.Fatalf("second Review() error = %v", err)
	}
	if captured.RoutingAffinity != nil {
		t.Fatalf("review routing affinity = %v, want none after an affinity-less parent response", captured.RoutingAffinity)
	}
	if got := captured.ClientMetadata["parent_response_id"]; got != "resp-parent-http" {
		t.Fatalf("parent_response_id = %q, want the newest response", got)
	}
}

// Rust #57710 retains the captured affinity through the review's retries even
// when the turn records a newer parent response in the meantime.
func TestGuardianReviewRetainsRoutingAffinityAcrossRetriesLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const threadID, turnID = "thread-guardian-affinity-retry", "turn-guardian-affinity-retry"
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}
	affinity := guardianTestRoutingAffinity(t, "retry-edge")
	router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
		Kind:            model.ResponsesStreamEventCreated,
		ResponseID:      "resp-retry-parent",
		RoutingAffinity: affinity,
	}, newResponsesStreamNotificationState(false, turnID))

	calls := 0
	var requests []*model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			requests = append(requests, request)
			if calls == 1 {
				// The parent turn moves on while the review is retrying.
				router.notifyResponsesStreamEvent(threadID, turnID, &model.ResponsesStreamEvent{
					Kind:            model.ResponsesStreamEventCreated,
					ResponseID:      "resp-retry-parent-2",
					RoutingAffinity: guardianTestRoutingAffinity(t, "newer-edge"),
				}, newResponsesStreamNotificationState(false, turnID))
				return nil, &codexapi.APIError{Kind: codexapi.ErrorServerOverloaded, Status: 503}
			}
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:          state.NewReviewStore(),
		parentResponse: router.guardianLatestParentResponseForTurn,
	}
	if _, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-1", state.Action{Type: "command", Command: "ls", CWD: "/tmp"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("review attempts = %d, want 2", calls)
	}
	for index, request := range requests {
		if request.RoutingAffinity != affinity {
			t.Fatalf("attempt %d routing affinity = %v, want the pin captured at review start", index+1, request.RoutingAffinity)
		}
		if got := request.ClientMetadata["parent_response_id"]; got != "resp-retry-parent" {
			t.Fatalf("attempt %d parent_response_id = %q, want the review-start parent", index+1, got)
		}
	}
}
