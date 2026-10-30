package appserver

import (
	"testing"
	"time"

	"codex_go/model"
	"codex_go/turn"
)

// TestComputerUseReviewPolicyOverridesLegacyFlagLikeRust mirrors Rust #43447: a
// model-owned Guardian computer-use policy takes precedence over the legacy
// node_repl_auto_review_required bit wherever review evidence or turn metadata
// asks whether computer use needs review.
func TestComputerUseReviewPolicyOverridesLegacyFlagLikeRust(t *testing.T) {
	adaptive := model.GuardianReviewModeAdaptive
	disabled := model.GuardianReviewModeDisabled
	if nodeReplAutoReviewRequiredForModel(&model.ModelInfo{
		NodeReplAutoReviewRequired: true,
		Guardian:                   &model.GuardianModelPolicy{ComputerUse: &disabled},
	}) {
		t.Fatal("legacy flag beat a disabled computer-use policy")
	}
	if !nodeReplAutoReviewRequiredForModel(&model.ModelInfo{
		Guardian: &model.GuardianModelPolicy{ComputerUse: &adaptive},
	}) {
		t.Fatal("adaptive policy did not require review without the legacy flag")
	}
	if !nodeReplAutoReviewRequiredForModel(&model.ModelInfo{NodeReplAutoReviewRequired: true}) {
		t.Fatal("legacy flag did not apply when the model has no policy")
	}
}

// TestGuardianReviewUsesCapturedComputerUsePolicyLikeRust checks the captured
// action metadata path, not just the model helper: the captured disabled policy
// must win over its captured legacy flag.
func TestGuardianReviewUsesCapturedComputerUsePolicyLikeRust(t *testing.T) {
	const (
		threadID = "thread-cua-policy"
		turnID   = "turn-cua-policy"
	)
	disabled := model.GuardianReviewModeDisabled
	router := NewRuntimeRouter(RuntimeServices{ThreadStatus: NewThreadStatusManager()})
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID: threadID,
		Model:    "gpt-test",
	}); err != nil {
		t.Fatalf("register active turn: %v", err)
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{
		Model: "gpt-test",
		ModelInfo: &model.ModelInfo{
			Slug:                       "gpt-test",
			Visibility:                 "list",
			SupportedInAPI:             true,
			NodeReplAutoReviewRequired: true,
			Guardian:                   &model.GuardianModelPolicy{ComputerUse: &disabled},
		},
	})
	if router.guardianReviewNodeReplAutoReviewRequiredForTurn(threadID, turnID) {
		t.Fatal("captured legacy flag beat the captured disabled computer-use policy")
	}
}
