package appserver

import (
	"testing"
	"time"

	"codex_go/model"
	"codex_go/turn"
)

// TestGuardianReviewerRespectsCatalogOriginsLikeRust covers Rust #57770: a
// bundled reviewer entry is not selectable for a custom gateway, while a
// provider-advertised or explicitly configured entry is.
func TestGuardianReviewerRespectsCatalogOriginsLikeRust(t *testing.T) {
	preferred := model.DefaultApprovalReviewPreferredModel
	parent := model.ModelInfo{
		Slug:                     guardianCatalogParentModel,
		Visibility:               "list",
		SupportedInAPI:           true,
		DefaultReasoningLevel:    "high",
		SupportedReasoningLevels: []string{"low", "medium", "high"},
	}
	reviewer := model.ModelInfo{
		Slug:                     preferred,
		Visibility:               "list",
		SupportedInAPI:           true,
		DefaultReasoningLevel:    "medium",
		SupportedReasoningLevels: []string{"low", "medium"},
	}
	cases := []struct {
		name       string
		origin     model.ModelOrigin
		wantModel  string
		wantReview bool
	}{
		{name: "provider catalog", origin: model.ModelOriginProvider, wantModel: preferred, wantReview: true},
		{name: "explicit config", origin: model.ModelOriginExplicitConfig, wantModel: preferred, wantReview: true},
		{name: "bundled catalog", origin: model.ModelOriginBundled, wantModel: guardianCatalogParentModel, wantReview: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manager := model.NewStaticModelsManagerWithOrigin(
				model.ModelsResponse{Models: []model.ModelInfo{parent, reviewer}}, testCase.origin,
			)
			router := newGuardianCatalogRouter(manager)
			const threadID, turnID = "thread-catalog-origin", "turn-catalog-origin"
			if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
				ThreadID: threadID,
				Model:    guardianCatalogParentModel,
			}); err != nil {
				t.Fatalf("register active turn: %v", err)
			}
			router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{Model: guardianCatalogParentModel})

			plan := router.guardianReviewPlanForTurn(threadID, turnID)
			if plan.Selection.Model != testCase.wantModel {
				t.Fatalf("selection model = %q, want %q", plan.Selection.Model, testCase.wantModel)
			}
			if got := plan.Selection.CatalogContainsAutoReview; got != testCase.wantReview {
				t.Fatalf("catalog contains reviewer = %v, want %v", got, testCase.wantReview)
			}

		})
	}
}
