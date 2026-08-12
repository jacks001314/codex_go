package historycell

import (
	"strconv"
	"strings"

	"codex_go/shell"

	"github.com/rivo/uniseg"
)

// Rust parity: codex-rs/tui/src/history_cell/approvals.rs.

type ReviewDecision string

const (
	ReviewApproved                    ReviewDecision = "approved"
	ReviewApprovedExecpolicyAmendment ReviewDecision = "approvedExecpolicyAmendment"
	ReviewApprovedForSession          ReviewDecision = "approvedForSession"
	ReviewNetworkPolicyAmendment      ReviewDecision = "networkPolicyAmendment"
	ReviewDenied                      ReviewDecision = "denied"
	ReviewTimedOut                    ReviewDecision = "timedOut"
	ReviewAbort                       ReviewDecision = "abort"
)

type ApprovalDecisionActor string

const (
	ApprovalActorUser     ApprovalDecisionActor = "user"
	ApprovalActorGuardian ApprovalDecisionActor = "guardian"
)

type NetworkPolicyRuleAction string

const (
	NetworkPolicyRuleAllow NetworkPolicyRuleAction = "allow"
	NetworkPolicyRuleDeny  NetworkPolicyRuleAction = "deny"
)

type ApprovalDecisionSubject struct {
	Command             []string
	NetworkTarget       string
	NetworkPolicyAction NetworkPolicyRuleAction
}

func NewCommandApprovalSubject(command []string) ApprovalDecisionSubject {
	return ApprovalDecisionSubject{Command: append([]string(nil), command...)}
}

func NewNetworkApprovalSubject(target string) ApprovalDecisionSubject {
	return ApprovalDecisionSubject{NetworkTarget: target}
}

func NewNetworkPolicyApprovalSubject(target string, action NetworkPolicyRuleAction) ApprovalDecisionSubject {
	return ApprovalDecisionSubject{NetworkTarget: target, NetworkPolicyAction: action}
}

func NewApprovalDecisionCell(subject ApprovalDecisionSubject, decision ReviewDecision, actor ApprovalDecisionActor) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell(approvalDecisionSummary(subject, decision, actor), approvalDecisionSymbol(subject, decision), "  ")
}

func NewGuardianDeniedPatchRequest(files []string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell(guardianPatchSummary("Request denied for codex to apply ", files), "\u2717 ", "  ")
}

func NewGuardianDeniedActionRequest(summary string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell("Request denied for "+summary, "\u2717 ", "  ")
}

func NewGuardianTimedOutPatchRequest(files []string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell(guardianPatchSummary("Review timed out before codex could apply ", files), "\u2717 ", "  ")
}

func NewGuardianTimedOutActionRequest(summary string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell("Review timed out before "+summary, "\u2717 ", "  ")
}

func NewReviewStatusLine(message string) PlainHistoryCell {
	return NewPlainHistoryCell([]string{message})
}

func approvalDecisionSummary(subject ApprovalDecisionSubject, decision ReviewDecision, actor ApprovalDecisionActor) string {
	actorSubject := approvalActorSubject(actor)
	commandSnippet := nonEmptyExecSnippet(subject.Command)
	target := subject.NetworkTarget
	isNetwork := target != ""
	switch decision {
	case ReviewApproved:
		if isNetwork {
			return actorSubject + "approved codex network access to " + target + " this time"
		}
		if commandSnippet != "" {
			return actorSubject + "approved codex to run " + commandSnippet + " this time"
		}
		return actorSubject + "approved this request this time"
	case ReviewApprovedExecpolicyAmendment:
		return actorSubject + "approved codex to always run commands that start with " + execSnippet(subject.Command)
	case ReviewApprovedForSession:
		if isNetwork {
			return actorSubject + "approved codex network access to " + target + " every time this session"
		}
		if commandSnippet != "" {
			return actorSubject + "approved codex to run " + commandSnippet + " every time this session"
		}
		return actorSubject + "approved this request every time this session"
	case ReviewNetworkPolicyAmendment:
		if target == "" {
			target = commandSnippet
		}
		if subject.NetworkPolicyAction == NetworkPolicyRuleDeny {
			return actorSubject + "denied codex network access to " + target + " and saved that rule"
		}
		return actorSubject + "persisted Codex network access to " + target
	case ReviewDenied:
		if isNetwork {
			return actorSubject + "did not approve codex network access to " + target
		}
		if commandSnippet != "" {
			if actor == ApprovalActorGuardian {
				return "Request denied for codex to run " + commandSnippet
			}
			return actorSubject + "did not approve codex to run " + commandSnippet
		}
		if actor == ApprovalActorGuardian {
			return "Request denied"
		}
		return actorSubject + "did not approve this request"
	case ReviewTimedOut:
		if isNetwork {
			return "Review timed out before codex could access " + target
		}
		if commandSnippet != "" {
			return "Review timed out before codex could run " + commandSnippet
		}
		return "Review timed out before this request could be approved"
	case ReviewAbort:
		if isNetwork {
			return actorSubject + "canceled the request for codex network access to " + target
		}
		if commandSnippet != "" {
			return actorSubject + "canceled the request to run " + commandSnippet
		}
		return actorSubject + "canceled this request"
	default:
		if commandSnippet != "" {
			return actorSubject + string(decision) + " " + commandSnippet
		}
		return actorSubject + string(decision)
	}
}

func approvalDecisionSymbol(subject ApprovalDecisionSubject, decision ReviewDecision) string {
	if decision == ReviewDenied || decision == ReviewTimedOut || decision == ReviewAbort ||
		(decision == ReviewNetworkPolicyAmendment && subject.NetworkPolicyAction == NetworkPolicyRuleDeny) {
		return "\u2717 "
	}
	return "\u2714 "
}

func approvalActorSubject(actor ApprovalDecisionActor) string {
	if actor == ApprovalActorGuardian {
		return "Auto-reviewer "
	}
	return "You "
}

func guardianPatchSummary(prefix string, files []string) string {
	if len(files) == 1 {
		return prefix + "a patch touching " + files[0]
	}
	return prefix + "a patch touching " + strconv.Itoa(len(files)) + " files"
}

func nonEmptyExecSnippet(command []string) string {
	snippet := execSnippet(command)
	if snippet == "" {
		return ""
	}
	return snippet
}

func execSnippet(command []string) string {
	if len(command) == 0 {
		return ""
	}
	full := shell.StripShellCommandAndEscape(command)
	if first, _, ok := strings.Cut(full, "\n"); ok {
		full = first + " ..."
	}
	return truncateApprovalSnippet(full, 80)
}

func truncateApprovalSnippet(text string, maxGraphemes int) string {
	if maxGraphemes <= 0 {
		return ""
	}
	overflow, cutAtMax := approvalGraphemeBoundaryAfter(text, maxGraphemes)
	if !overflow {
		return text
	}
	if maxGraphemes < 3 {
		return text[:cutAtMax]
	}
	_, cutAtPrefix := approvalGraphemeBoundaryAfter(text, maxGraphemes-3)
	return text[:cutAtPrefix] + "..."
}

func approvalGraphemeBoundaryAfter(text string, count int) (bool, int) {
	if count <= 0 {
		return text != "", 0
	}
	graphemes := uniseg.NewGraphemes(text)
	seen := 0
	cut := len(text)
	for graphemes.Next() {
		if seen == count {
			start, _ := graphemes.Positions()
			return true, start
		}
		seen++
		_, end := graphemes.Positions()
		if seen == count {
			cut = end
		}
	}
	return false, cut
}
