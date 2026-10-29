package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
	"codex_go/turn"
)

// TestGuardianReviewSessionCarriesWholeReviewModelLikeRust mirrors Rust #45684:
// one ReviewModel selection travels through the guardian review session intact,
// so session execution, analytics, and failed-review records read the same
// model, effort, and selection metadata instead of re-resolving pieces of it.
func TestGuardianReviewSessionCarriesWholeReviewModelLikeRust(t *testing.T) {
	const (
		threadID      = "thread-review-model-struct"
		turnID        = "turn-review-model-struct"
		parentModel   = "gpt-parent"
		overrideModel = "gpt-review-override"
		overrideHash  = "comphash-review-override"
	)
	preferredModel := model.DefaultApprovalReviewPreferredModel
	catalog := []model.ModelInfo{
		{
			Slug:                     parentModel,
			Visibility:               "list",
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "high",
			SupportedReasoningLevels: []string{"low", "medium", "high"},
			AutoReviewModelOverride:  overrideModel,
		},
		{
			Slug:                     preferredModel,
			Visibility:               "list",
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium"},
		},
		{
			Slug:                     overrideModel,
			Visibility:               "list",
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium"},
			CompHash:                 overrideHash,
		},
	}
	var captured *model.AgentRequest
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		copyRequest := *request
		captured = &copyRequest
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"review model struct"}`}, nil
	})
	router := NewRuntimeRouter(RuntimeServices{
		Agent:        agent,
		Models:       model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: catalog})),
		ThreadStatus: NewThreadStatusManager(),
	})
	effort := "high"
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID: threadID,
		Model:    parentModel,
		Effort:   &effort,
	}); err != nil {
		t.Fatalf("register active turn: %v", err)
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{Model: parentModel})

	plan := router.guardianReviewPlanForTurn(threadID, turnID)
	want := model.ApprovalReviewModel{
		Model:                     overrideModel,
		ReasoningEffort:           "low",
		DefaultReviewModelID:      preferredModel,
		CatalogContainsAutoReview: true,
		ModelOverridden:           true,
		ModelOverride:             overrideModel,
	}
	if plan.Selection != want {
		t.Fatalf("plan selection = %#v, want %#v", plan.Selection, want)
	}

	reviewer := router.ensureGuardianReviewer(agent)
	if _, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-model-struct", state.Action{
		Type: "mcp_tool_call", Server: "apps", ToolName: "calendar",
	}); err != nil {
		t.Fatalf("Guardian review: %v", err)
	}
	if captured == nil || captured.Model != plan.Selection.Model || captured.ReasoningEffort != plan.Selection.ReasoningEffort {
		t.Fatalf("Guardian request = %#v, want model %q effort %q from the plan selection", captured, plan.Selection.Model, plan.Selection.ReasoningEffort)
	}
	// The catalog-hash helper keys off the same selection instead of resolving
	// the reviewer again from the parent model.
	if hash := router.guardianReviewModelHashForTurn(threadID, turnID); hash != overrideHash {
		t.Fatalf("review model hash = %q, want %q", hash, overrideHash)
	}
}
