package exec

import (
	"bytes"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

// TestExecHumanApprovalWarningLikeRust pins the Rust #63515 warning: exec tells
// the user when the thread asks a human, and only the resumable hint lines
// depend on whether config flags can change the saved settings.
func TestExecHumanApprovalWarningLikeRust(t *testing.T) {
	var stderr bytes.Buffer
	warnIfHumanApproval(&stderr, sandbox.ApprovalOnRequest, string(config.ApprovalsReviewerUser), true)
	got := stderr.String()
	for _, want := range []string{
		"WARNING: This thread is configured to ask a human for approval.",
		"`codex exec` runs non-interactively and immediately rejects approval requests it receives.",
		"- To review requests yourself, run this turn from the TUI.",
		"- To change the thread's saved reviewer, pass `--config 'approvals_reviewer=\"auto_review\"'` to `codex exec resume`.",
		"- To disable approval prompts for this and future turns, pass `--config 'approval_policy=\"never\"'` to `codex exec resume`.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning missing %q: %q", want, got)
		}
	}

	// An untrusted project cannot be switched to Never without also allowing
	// ordinary sandboxed commands that currently need approval, so Rust omits
	// the config hints there.
	stderr.Reset()
	warnIfHumanApproval(&stderr, sandbox.ApprovalUnlessTrusted, string(config.ApprovalsReviewerUser), true)
	got = stderr.String()
	if !strings.Contains(got, "WARNING: This thread is configured to ask a human for approval.") {
		t.Fatalf("untrusted warning missing: %q", got)
	}
	if strings.Contains(got, "--config") {
		t.Fatalf("untrusted warning offered a policy switch: %q", got)
	}

	// Remote exec cannot change the thread's saved settings from here.
	stderr.Reset()
	warnIfHumanApproval(&stderr, sandbox.ApprovalOnRequest, string(config.ApprovalsReviewerUser), false)
	got = stderr.String()
	if !strings.Contains(got, "WARNING: This thread is configured to ask a human for approval.") || strings.Contains(got, "--config") {
		t.Fatalf("remote warning = %q", got)
	}

	// Auto-review and never-policy runs never ask a human.
	for _, review := range []struct {
		policy   sandbox.AskForApproval
		reviewer string
	}{
		{sandbox.ApprovalOnRequest, string(config.ApprovalsReviewerAutoReview)},
		{sandbox.ApprovalNever, string(config.ApprovalsReviewerUser)},
	} {
		stderr.Reset()
		warnIfHumanApproval(&stderr, review.policy, review.reviewer, true)
		if stderr.Len() != 0 {
			t.Fatalf("policy %s reviewer %s warned: %q", review.policy, review.reviewer, stderr.String())
		}
	}
}
