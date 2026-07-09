package historycell

import (
	"strings"

	"codex_go/internal/tui"
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

type ApprovalDecisionSubject struct {
	Command       []string
	NetworkTarget string
}

func NewCommandApprovalSubject(command []string) ApprovalDecisionSubject {
	return ApprovalDecisionSubject{Command: append([]string(nil), command...)}
}

func NewNetworkApprovalSubject(target string) ApprovalDecisionSubject {
	return ApprovalDecisionSubject{NetworkTarget: strings.TrimSpace(target)}
}

func NewApprovalDecisionCell(subject ApprovalDecisionSubject, decision ReviewDecision, actor ApprovalDecisionActor) PrefixedWrappedHistoryCell {
	symbol := "\u2713 "
	if decision == ReviewDenied || decision == ReviewTimedOut || decision == ReviewAbort {
		symbol = "\u2717 "
	}
	return NewPrefixedWrappedHistoryCell(approvalDecisionSummary(subject, decision, actor), symbol, "  ")
}

func NewGuardianDeniedPatchRequest(files []string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell(guardianPatchSummary("Request denied for codex to apply ", files), "\u2717 ", "  ")
}

func NewGuardianDeniedActionRequest(summary string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell("Request denied for "+strings.TrimSpace(summary), "\u2717 ", "  ")
}

func NewGuardianApprovedActionRequest(summary string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell("Request approved for "+strings.TrimSpace(summary), "\u2713 ", "  ")
}

func NewGuardianTimedOutPatchRequest(files []string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell(guardianPatchSummary("Review timed out before codex could apply ", files), "\u2717 ", "  ")
}

func NewGuardianTimedOutActionRequest(summary string) PrefixedWrappedHistoryCell {
	return NewPrefixedWrappedHistoryCell("Review timed out before "+strings.TrimSpace(summary), "\u2717 ", "  ")
}

func NewReviewStatusLine(message string) PlainHistoryCell {
	return NewPlainHistoryCell([]string{strings.TrimSpace(message)})
}

func approvalDecisionSummary(subject ApprovalDecisionSubject, decision ReviewDecision, actor ApprovalDecisionActor) string {
	actorSubject := approvalActorSubject(actor)
	commandSnippet := nonEmptyExecSnippet(subject.Command)
	target := strings.TrimSpace(subject.NetworkTarget)
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
		if commandSnippet != "" {
			return actorSubject + "approved codex to always run commands that start with " + commandSnippet
		}
		return actorSubject + "approved codex to always run matching commands"
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

func approvalActorSubject(actor ApprovalDecisionActor) string {
	if actor == ApprovalActorGuardian {
		return "Auto-reviewer "
	}
	return "You "
}

func guardianPatchSummary(prefix string, files []string) string {
	cleaned := make([]string, 0, len(files))
	for _, file := range files {
		if trimmed := strings.TrimSpace(file); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 1 {
		return prefix + "a patch touching " + cleaned[0]
	}
	return prefix + "a patch touching " + tui.FormatInt(int64(len(cleaned))) + " files"
}

func nonEmptyExecSnippet(command []string) string {
	snippet := execSnippet(command)
	if strings.TrimSpace(snippet) == "" {
		return ""
	}
	return snippet
}

func execSnippet(command []string) string {
	if len(command) == 0 {
		return ""
	}
	full := strings.Join(command, " ")
	if len(command) >= 3 {
		shell := strings.ToLower(strings.ReplaceAll(command[0], "\\", "/"))
		if (strings.HasSuffix(shell, "/bash") || strings.HasSuffix(shell, "/sh") || strings.HasSuffix(shell, "/zsh") || strings.HasSuffix(shell, "/fish") || shell == "bash" || shell == "sh" || shell == "zsh" || shell == "fish") && command[1] == "-lc" {
			full = command[2]
		}
	}
	if first, _, ok := strings.Cut(full, "\n"); ok {
		full = first + " ..."
	}
	return truncateApprovalSnippet(strings.TrimSpace(full), 80)
}

func truncateApprovalSnippet(text string, maxRunes int) string {
	runes := []rune(text)
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return text
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "\u2026"
}
