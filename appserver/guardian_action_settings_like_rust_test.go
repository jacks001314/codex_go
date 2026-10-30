package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/state"
	"codex_go/turn"
)

// Rust #44574 configures each Guardian review from the reviewed action's
// captured settings: the captured model (not the live turn parameter), its
// metadata for computer-use review behavior, and the captured reasoning summary.
func TestGuardianReviewUsesCapturedActionSettingsLikeRust(t *testing.T) {
	const (
		threadID      = "thread-captured-settings"
		turnID        = "turn-captured-settings"
		liveModel     = "gpt-live"
		capturedModel = "gpt-captured"
	)
	catalog := []model.ModelInfo{
		{
			Slug:                     liveModel,
			Visibility:               "list",
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium"},
		},
		{
			Slug:                     capturedModel,
			Visibility:               "list",
			SupportedInAPI:           true,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium"},
		},
	}
	var captured *model.AgentRequest
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		clone := *request
		captured = &clone
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
	})
	router := NewRuntimeRouter(RuntimeServices{
		Agent:        agent,
		Models:       model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: catalog})),
		ThreadStatus: NewThreadStatusManager(),
	})
	summary := "concise"
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID: threadID,
		Model:    liveModel,
		Summary:  &summary,
	}); err != nil {
		t.Fatalf("register active turn: %v", err)
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{
		Model:            capturedModel,
		ReasoningSummary: "detailed",
		// The captured metadata says the action needs computer-use review while
		// the current catalog entry does not; the review must follow the action.
		ModelInfo: &model.ModelInfo{
			Slug:                       capturedModel,
			Visibility:                 "list",
			SupportedInAPI:             true,
			DefaultReasoningLevel:      "medium",
			SupportedReasoningLevels:   []string{"low", "medium"},
			NodeReplAutoReviewRequired: true,
		},
	})

	// Computer-use review behavior reads the action's captured model metadata,
	// not the live turn parameter that still names the previous model.
	if !router.guardianReviewNodeReplAutoReviewRequiredForTurn(threadID, turnID) {
		t.Fatal("computer-use review behavior did not use the captured model metadata")
	}
	plan := router.guardianReviewPlanForTurn(threadID, turnID)
	if plan.Selection.Model != capturedModel {
		t.Fatalf("review model = %q, want the captured %q", plan.Selection.Model, capturedModel)
	}
	if plan.ReasoningSummary != "detailed" {
		t.Fatalf("review reasoning summary = %q, want the captured %q", plan.ReasoningSummary, "detailed")
	}

	reviewer := router.ensureGuardianReviewer(agent)
	if _, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-captured-settings", state.Action{
		Type:    "command",
		Command: "ls",
		CWD:     t.TempDir(),
	}); err != nil {
		t.Fatalf("Guardian review: %v", err)
	}
	if captured == nil || captured.Model != capturedModel {
		t.Fatalf("Guardian request model = %#v, want the captured %q", captured, capturedModel)
	}
	if captured.ReasoningSummary != "detailed" {
		t.Fatalf("Guardian request reasoning summary = %q, want the captured %q", captured.ReasoningSummary, "detailed")
	}

	// Without a resolved run-config summary the turn's captured parameter is the
	// fallback, still matching the action rather than the live config.
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{Model: capturedModel})
	if plan := router.guardianReviewPlanForTurn(threadID, turnID); plan.ReasoningSummary != summary {
		t.Fatalf("review reasoning summary fallback = %q, want %q", plan.ReasoningSummary, summary)
	}
}
