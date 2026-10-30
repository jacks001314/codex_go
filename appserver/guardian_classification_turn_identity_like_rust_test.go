package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/state"
	"codex_go/turn"

	"github.com/google/uuid"
)

// guardianClassificationClientMetadata decodes the turn-metadata header of one
// review request so a test can compare it with the top-level client metadata.
func guardianClassificationClientMetadata(t *testing.T, request *model.AgentRequest) map[string]any {
	t.Helper()
	if request == nil {
		t.Fatal("reviewer did not sample")
	}
	raw := strings.TrimSpace(request.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader])
	if raw == "" {
		t.Fatalf("review request has no turn metadata: %#v", request.ClientMetadata)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("turn metadata json error = %v document=%q", err, raw)
	}
	return document
}

// assertGuardianClassificationIdentity mirrors Rust #41385's assert_turn_id:
// turn_id is the classification's own UUIDv7 turn, parent_turn_id is the
// reviewed turn, and root_turn_id appears in both client_metadata and
// x-codex-turn-metadata only when the reviewed turn recorded a causal root.
func assertGuardianClassificationIdentity(t *testing.T, request *model.AgentRequest, parentTurnID, rootTurnID string) string {
	t.Helper()
	document := guardianClassificationClientMetadata(t, request)
	turnID := strings.TrimSpace(request.ClientMetadata["turn_id"])
	if turnID == "" {
		t.Fatalf("classification turn_id missing: %#v", request.ClientMetadata)
	}
	id, err := uuid.Parse(turnID)
	if err != nil || id.Version() != 7 {
		t.Fatalf("classification turn_id = %q (%v), want a UUIDv7", turnID, err)
	}
	if turnID == parentTurnID {
		t.Fatalf("classification reuses the owning turn id %q", parentTurnID)
	}
	if request.TurnID != turnID {
		t.Fatalf("request turn id = %q, want classification turn %q", request.TurnID, turnID)
	}
	if document["turn_id"] != turnID {
		t.Fatalf("turn metadata turn_id = %#v, want %q", document["turn_id"], turnID)
	}
	if got := request.ClientMetadata["parent_turn_id"]; got != parentTurnID {
		t.Fatalf("parent_turn_id = %q, want %q", got, parentTurnID)
	}
	if document["parent_turn_id"] != parentTurnID {
		t.Fatalf("turn metadata parent_turn_id = %#v, want %q", document["parent_turn_id"], parentTurnID)
	}
	if rootTurnID == "" {
		if _, ok := request.ClientMetadata["root_turn_id"]; ok {
			t.Fatalf("root_turn_id must be omitted for an unknown lineage: %#v", request.ClientMetadata)
		}
		if _, ok := document["root_turn_id"]; ok {
			t.Fatalf("turn metadata root_turn_id must be omitted for an unknown lineage: %#v", document)
		}
		return turnID
	}
	if got := request.ClientMetadata["root_turn_id"]; got != rootTurnID {
		t.Fatalf("root_turn_id = %q, want %q", got, rootTurnID)
	}
	if document["root_turn_id"] != rootTurnID {
		t.Fatalf("turn metadata root_turn_id = %#v, want %q", document["root_turn_id"], rootTurnID)
	}
	return turnID
}

// Mirrors Rust #41385: consecutive Guardian classifications are distinct turns
// whose parent is the turn that requested each review and whose root is that
// turn's trusted causal root.
func TestGuardianClassificationsUseDistinctTurnIdentitiesLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const threadID = "thread-guardian-classification-id"

	var requests []*model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			copyRequest := *request
			requests = append(requests, &copyRequest)
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:             state.NewReviewStore(),
		trustedRootTurnID: router.guardianTrustedRootTurnIDForTurn,
	}

	classify := func(turnID string) string {
		if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
			ThreadID:   threadID,
			RootTurnID: "causal-root",
		}); err != nil {
			t.Fatalf("registerActiveRuntimeTurn(%q) error = %v", turnID, err)
		}
		t.Cleanup(func() { router.threads.ConsumeTurn(threadID, turnID, false) })
		before := len(requests)
		if _, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-"+turnID, state.Action{Type: "command", Command: "ls", CWD: "/tmp"}); err != nil {
			t.Fatalf("Review(%q) error = %v", turnID, err)
		}
		if len(requests) != before+1 {
			t.Fatalf("Review(%q) sampled %d requests, want 1", turnID, len(requests)-before)
		}
		return assertGuardianClassificationIdentity(t, requests[len(requests)-1], turnID, "causal-root")
	}

	first := classify("turn-owner-1")
	if _, ok := router.threads.ConsumeTurn(threadID, "turn-owner-1", false); !ok {
		t.Fatal("ConsumeTurn() did not find the first owning turn")
	}
	second := classify("turn-owner-2")
	if first == second {
		t.Fatalf("consecutive classifications reused turn id %q", first)
	}
}

// Mirrors Rust #41385's retry coverage: every attempt of one classification
// keeps the same reviewer turn id and the owning turn stays its parent.
func TestGuardianClassificationKeepsTurnIdentityAcrossRetriesLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const threadID, turnID = "thread-guardian-classification-retry", "turn-owner-retry"
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID:   threadID,
		RootTurnID: "retry-root",
	}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}

	calls := 0
	var requests []*model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			calls++
			copyRequest := *request
			requests = append(requests, &copyRequest)
			if calls == 1 {
				return nil, &codexapi.APIError{Kind: codexapi.ErrorServerOverloaded, Status: 503}
			}
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:             state.NewReviewStore(),
		trustedRootTurnID: router.guardianTrustedRootTurnIDForTurn,
	}
	if _, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-retry", state.Action{Type: "command", Command: "ls", CWD: "/tmp"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if calls != 2 || len(requests) != 2 {
		t.Fatalf("review attempts = %d/%d, want 2", calls, len(requests))
	}
	first := assertGuardianClassificationIdentity(t, requests[0], turnID, "retry-root")
	second := assertGuardianClassificationIdentity(t, requests[1], turnID, "retry-root")
	if first != second {
		t.Fatalf("retry changed the classification turn id: %q then %q", first, second)
	}
}

// Mirrors Rust #41385's omission rule: a turn without recorded lineage does not
// advertise its own id as the review's causal root.
func TestGuardianClassificationOmitsUnknownRootLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const threadID, turnID = "thread-guardian-classification-rootless", "turn-owner-rootless"
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}

	var captured *model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			copyRequest := *request
			captured = &copyRequest
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:             state.NewReviewStore(),
		trustedRootTurnID: router.guardianTrustedRootTurnIDForTurn,
	}
	if _, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-rootless", state.Action{Type: "command", Command: "ls", CWD: "/tmp"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	assertGuardianClassificationIdentity(t, captured, turnID, "")
}

// Mirrors Rust #41385 for the isolated assessment path: the captured action's
// turn is the parent, and the isolated reviewer samples under its own turn.
func TestGuardianIsolatedAssessmentUsesDistinctTurnIdentityLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	const threadID, turnID = "thread-guardian-isolated-identity", "turn-owner-isolated"
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID:   threadID,
		RootTurnID: "isolated-root",
	}); err != nil {
		t.Fatalf("registerActiveRuntimeTurn() error = %v", err)
	}

	var captured *model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			copyRequest := *request
			captured = &copyRequest
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:             state.NewReviewStore(),
		trustedRootTurnID: router.guardianTrustedRootTurnIDForTurn,
	}
	if _, err := reviewer.AssessIsolated(context.Background(), guardianIsolatedReviewInput{
		ThreadID: threadID,
		TurnID:   turnID,
		Action:   state.Action{Type: "command", Command: "ls", CWD: "/tmp"},
	}, time.Minute); err != nil {
		t.Fatalf("AssessIsolated() error = %v", err)
	}
	assertGuardianClassificationIdentity(t, captured, turnID, "isolated-root")
}
