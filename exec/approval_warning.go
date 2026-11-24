package exec

import (
	"fmt"
	"io"
	"strings"

	"codex_go/config"
	"codex_go/sandbox"
)

// warnIfHumanApproval explains how `codex exec` handles approvals when the
// effective settings call for a human reviewer (Rust exec/src/approval_warning.rs,
// #63515). Exec runs non-interactively, so it rejects approval requests it
// receives; the warning tells the user before a turn hits one.
func warnIfHumanApproval(stderr io.Writer, policy sandbox.AskForApproval, reviewer string, flagsSupported bool) {
	if stderr == nil {
		return
	}
	// Even with every granular category disabled, a dangerous command can
	// require approval to run within the existing sandbox.
	if !strings.EqualFold(strings.TrimSpace(reviewer), string(config.ApprovalsReviewerUser)) ||
		policy == sandbox.ApprovalNever {
		return
	}
	fmt.Fprint(stderr, "WARNING: This thread is configured to ask a human for approval. "+
		"`codex exec` runs non-interactively and immediately rejects approval requests it receives.\n"+
		"- To review requests yourself, run this turn from the TUI.\n")
	// Switching an untrusted project to Never would also allow ordinary
	// sandboxed commands that currently require approval.
	if flagsSupported && policy != sandbox.ApprovalUnlessTrusted {
		fmt.Fprint(stderr, "- To change the thread's saved reviewer, pass `--config 'approvals_reviewer=\"auto_review\"'` to `codex exec resume`.\n"+
			"- To disable approval prompts for this and future turns, pass `--config 'approval_policy=\"never\"'` to `codex exec resume`.\n")
	}
}
