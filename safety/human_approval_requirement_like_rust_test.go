package safety

import (
	"strings"
	"testing"
)

// TestRequirementFromPromptUserEvaluationLikeRust pins Rust #58045: a
// prompt-user rule yields a fresh human-approval requirement with a human
// reason and no reusable exec-policy amendment.
func TestRequirementFromPromptUserEvaluationLikeRust(t *testing.T) {
	evaluation := Evaluation{
		Decision: DecisionPromptUser,
		MatchedRules: []RuleMatch{{
			Decision: DecisionPromptUser,
			IsPolicy: true,
			Prefix:   []string{"rm"},
			Reason:   "managed checkpoint",
		}},
	}
	requirement := RequirementFromEvaluation([]string{"rm", "-rf", "build"}, ApprovalPolicy{Mode: ApprovalOnRequest}, evaluation, []string{"rm"}, true)
	if requirement.Kind != ExecNeedsHumanApproval {
		t.Fatalf("kind = %q, want needs_human_approval", requirement.Kind)
	}
	if !strings.Contains(requirement.Reason, "requires human approval: managed checkpoint") {
		t.Fatalf("reason = %q", requirement.Reason)
	}
	if requirement.Amendment != nil {
		t.Fatalf("amendment = %#v, want nil", requirement.Amendment)
	}

	never := RequirementFromEvaluation([]string{"rm"}, ApprovalPolicy{Mode: ApprovalNever}, evaluation, nil, false)
	if never.Kind != ExecForbidden || never.Reason != PromptConflictReason {
		t.Fatalf("never policy requirement = %#v", never)
	}

	granular := RequirementFromEvaluation([]string{"rm"}, ApprovalPolicy{Mode: ApprovalGranular, Rules: false}, evaluation, nil, false)
	if granular.Kind != ExecForbidden || granular.Reason != RejectRulesApprovalReason {
		t.Fatalf("granular policy requirement = %#v", granular)
	}
}
