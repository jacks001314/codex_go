package exec

import (
	"bytes"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/rollout"
	"codex_go/sandbox"
)

// TestExecExplicitCLIApprovalsPersistOnThreadLikeRust checks Rust #63515's
// "explicit overrides are saved on the resulting thread": a CLI approval policy
// lands in the thread's settings snapshot, so a resume inherits it.
func TestExecExplicitCLIApprovalsPersistOnThreadLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
		t.Fatalf("Save auth returned error: %v", err)
	}
	runner := NewRunner(home)
	runner.Agent = &recordingAgent{message: "done"}
	var stdout, stderr bytes.Buffer
	result, err := runner.Run(Request{
		Exec: cli.ExecOptions{
			Prompt: "hello",
			Shared: cli.SharedOptions{ApprovalPolicy: string(sandbox.ApprovalNever)},
		},
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run returned error: %v stderr=%q", err, stderr.String())
	}
	path, err := rollout.FindThreadPath(home, result.ThreadID, false)
	if err != nil {
		t.Fatalf("FindThreadPath = %q, %v", path, err)
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	settings, ok := rollout.LatestPersistedResumeSettings(result.ThreadID, lines)
	if !ok {
		t.Fatal("the exec turn recorded no resume settings")
	}
	if settings.ApprovalPolicy != string(sandbox.ApprovalNever) {
		t.Fatalf("saved approval policy = %q, want never", settings.ApprovalPolicy)
	}
	if strings.TrimSpace(settings.ApprovalsReviewer) != string(config.ApprovalsReviewerUser) {
		t.Fatalf("saved reviewer = %q, want user", settings.ApprovalsReviewer)
	}

	// Without an explicit choice nothing is saved, so a resume keeps whatever
	// the thread already had.
	result, err = runner.Run(Request{Exec: cli.ExecOptions{Prompt: "again"}}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	path, err = rollout.FindThreadPath(home, result.ThreadID, false)
	if err != nil {
		t.Fatalf("FindThreadPath = %q, %v", path, err)
	}
	lines, _, err = rollout.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if _, ok := rollout.LatestPersistedResumeSettings(result.ThreadID, lines); ok {
		t.Fatal("a run without an explicit approval choice saved thread settings")
	}
}

// TestExecResumeInheritsSavedApprovalsLikeRust covers the resume half: the saved
// policy and reviewer survive unless this run selected its own, and a config
// layer that names an approval policy counts as an explicit choice.
func TestExecResumeInheritsSavedApprovalsLikeRust(t *testing.T) {
	state := &execResumePermissionState{
		Found:             true,
		ApprovalPolicy:    string(sandbox.ApprovalNever),
		ApprovalsReviewer: string(config.ApprovalsReviewerAutoReview),
	}
	policy, reviewer := resumeInheritedApprovals(
		&config.Config{Values: map[string]any{}},
		&Request{Exec: cli.ExecOptions{Subcommand: "resume"}},
		state,
		sandbox.ApprovalOnRequest,
		string(config.ApprovalsReviewerUser),
	)
	if policy != sandbox.ApprovalNever || reviewer != string(config.ApprovalsReviewerAutoReview) {
		t.Fatalf("inherited approvals = %q/%q, want never/auto_review", policy, reviewer)
	}

	// An explicit CLI policy wins over the saved one.
	policy, reviewer = resumeInheritedApprovals(
		&config.Config{Values: map[string]any{}},
		&Request{Exec: cli.ExecOptions{Subcommand: "resume", Shared: cli.SharedOptions{ApprovalPolicy: string(sandbox.ApprovalOnRequest)}}},
		state,
		sandbox.ApprovalOnRequest,
		string(config.ApprovalsReviewerUser),
	)
	if policy != sandbox.ApprovalOnRequest {
		t.Fatalf("CLI policy = %q, want on-request", policy)
	}
	// --approve-for-me is the explicit reviewer choice.
	if reviewer != string(config.ApprovalsReviewerAutoReview) {
		t.Fatalf("inherited reviewer with CLI policy = %q, want the saved reviewer", reviewer)
	}
	_, reviewer = resumeInheritedApprovals(
		&config.Config{Values: map[string]any{}},
		&Request{Exec: cli.ExecOptions{Subcommand: "resume", Shared: cli.SharedOptions{ApproveForMe: true}}},
		state,
		sandbox.ApprovalOnRequest,
		string(config.ApprovalsReviewerAutoReview),
	)
	if reviewer != string(config.ApprovalsReviewerAutoReview) {
		t.Fatalf("approve-for-me reviewer = %q", reviewer)
	}

	// A configured approval policy is an explicit choice too, exactly like
	// Rust's per-layer check.
	policy, _ = resumeInheritedApprovals(
		&config.Config{Values: map[string]any{"approval_policy": string(sandbox.ApprovalOnRequest)}},
		&Request{Exec: cli.ExecOptions{Subcommand: "resume"}},
		state,
		sandbox.ApprovalOnRequest,
		string(config.ApprovalsReviewerUser),
	)
	if policy != sandbox.ApprovalOnRequest {
		t.Fatalf("configured policy = %q, want on-request", policy)
	}

	// A thread without saved settings keeps the resolved defaults.
	policy, reviewer = resumeInheritedApprovals(
		&config.Config{Values: map[string]any{}},
		&Request{Exec: cli.ExecOptions{Subcommand: "resume"}},
		&execResumePermissionState{},
		sandbox.ApprovalOnRequest,
		string(config.ApprovalsReviewerUser),
	)
	if policy != sandbox.ApprovalOnRequest || reviewer != string(config.ApprovalsReviewerUser) {
		t.Fatalf("fresh resume approvals = %q/%q", policy, reviewer)
	}
}
