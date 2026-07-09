package tui

type ApprovalDecision string

const (
	ApprovalDecisionApprove ApprovalDecision = "approve"
	ApprovalDecisionDeny    ApprovalDecision = "deny"
	ApprovalDecisionAbort   ApprovalDecision = "abort"
)

func ApprovalDecisionFromBool(approved bool) ApprovalDecision {
	if approved {
		return ApprovalDecisionApprove
	}
	return ApprovalDecisionDeny
}
