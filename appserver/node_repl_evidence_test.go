package appserver

import (
	"testing"

	"codex_go/model"
)

func TestNodeReplAutoReviewRequiredForModelHonorsGuardianPolicy(t *testing.T) {
	adaptive := model.GuardianReviewModeAdaptive
	disabled := model.GuardianReviewModeDisabled
	if !nodeReplAutoReviewRequiredForModel(&model.ModelInfo{Guardian: &model.GuardianModelPolicy{ComputerUse: &adaptive}}) {
		t.Fatal("adaptive computer-use policy should require auto review")
	}
	if nodeReplAutoReviewRequiredForModel(&model.ModelInfo{Guardian: &model.GuardianModelPolicy{ComputerUse: &disabled}}) {
		t.Fatal("disabled computer-use policy should not require auto review")
	}
	if nodeReplAutoReviewRequiredForModel(nil) {
		t.Fatal("nil model info should not require auto review")
	}
}
