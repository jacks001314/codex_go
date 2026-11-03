package tool

import (
	"context"
	"strings"
	"testing"

	"codex_go/sandbox"
)

// sequencedShellRunner returns one scripted result per attempt and records the
// requests the executor actually launched.
type sequencedShellRunner struct {
	requests []*ShellRequest
	results  []*ShellResult
}

func (r *sequencedShellRunner) Run(ctx context.Context, req *ShellRequest) (*ShellResult, error) {
	_ = ctx
	r.requests = append(r.requests, req)
	if len(r.results) == 0 {
		return &ShellResult{}, nil
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result, nil
}

func sandboxDeniedResult(exitCode int, output string) *ShellResult {
	sandboxType := sandbox.SandboxTypeLinuxSeccomp
	return &ShellResult{
		ExitCode:      exitCode,
		HasExitCode:   true,
		Stdout:        output,
		SandboxType:   &sandboxType,
		SandboxDenied: true,
	}
}

// TestShellExecutorRetriesSandboxDeniedCommandAfterApprovalLikeRust pins Rust's
// orchestrator retry (core/src/tools/orchestrator.rs): a command the sandbox
// blocked is offered once more without the sandbox, behind the escalation
// approval that opens the operator's menu.
func TestShellExecutorRetriesSandboxDeniedCommandAfterApprovalLikeRust(t *testing.T) {
	profile := sandbox.WorkspaceWritePermissionProfile()
	runner := &sequencedShellRunner{results: []*ShellResult{
		sandboxDeniedResult(1, "touch: cannot touch 'x': Permission denied"),
		{ExitCode: 0, HasExitCode: true, Stdout: "touched\n"},
	}}
	var approvalRequest *ShellApprovalRequest
	approvals := 0
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:    sandbox.ApprovalOnRequest,
			CWD:               t.TempDir(),
			PermissionProfile: &profile,
		},
		Approval: func(ctx context.Context, request *ShellApprovalRequest) (ShellApprovalDecision, error) {
			_ = ctx
			approvals++
			approvalRequest = request
			return ShellApprovalDecision{Approved: true}, nil
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-sandbox-retry",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"touch x"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if approvals != 1 {
		t.Fatalf("approvals = %d, want 1", approvals)
	}
	if approvalRequest == nil || approvalRequest.Request == nil {
		t.Fatalf("approval request = %#v", approvalRequest)
	}
	if !approvalRequest.Request.ApprovalRequired || approvalRequest.Request.ApprovalReason != sandboxDenialRetryReason {
		t.Fatalf("approval request = %#v reason=%q", approvalRequest.Request, approvalRequest.Request.ApprovalReason)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("attempts = %d, want 2", len(runner.requests))
	}
	first, retry := runner.requests[0], runner.requests[1]
	if first.SandboxPermissions == sandbox.SandboxPermissionsRequireEscalated {
		t.Fatalf("first attempt already escalated: %#v", first)
	}
	if retry.SandboxPermissions != sandbox.SandboxPermissionsRequireEscalated {
		t.Fatalf("retry sandbox permissions = %q", retry.SandboxPermissions)
	}
	if retry.ApprovalRequired {
		t.Fatalf("retry still requires approval: %#v", retry)
	}
	if retry.PermissionProfileID != sandbox.BuiltInPermissionProfileDangerFullAccess {
		t.Fatalf("retry permission profile = %q", retry.PermissionProfileID)
	}
	if !output.Success || !strings.Contains(output.Body, "touched") || output.SandboxDenied {
		t.Fatalf("output = %#v", output)
	}
}

func TestShellExecutorKeepsSandboxDenialWhenRetryApprovalIsDeniedLikeRust(t *testing.T) {
	profile := sandbox.WorkspaceWritePermissionProfile()
	runner := &sequencedShellRunner{results: []*ShellResult{
		sandboxDeniedResult(1, "bash: /workspace/locked: Permission denied"),
	}}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:    sandbox.ApprovalOnRequest,
			CWD:               t.TempDir(),
			PermissionProfile: &profile,
		},
		Approval: func(ctx context.Context, request *ShellApprovalRequest) (ShellApprovalDecision, error) {
			_ = ctx
			_ = request
			return ShellApprovalDecision{DenyReason: "no"}, nil
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-sandbox-retry-denied",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"touch x"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("attempts = %d, want 1", len(runner.requests))
	}
	if !output.Success || !output.SandboxDenied {
		t.Fatalf("output = %#v", output)
	}
	if !strings.Contains(output.Body, "Permission denied") {
		t.Fatalf("body = %q", output.Body)
	}
}

// TestShellExecutorReportsSandboxDenialWithoutApprovalCallbackLikeRust covers
// the non-interactive carrier (no approval callback): Rust reports the denial
// to the model instead of retrying when nobody can answer a prompt.
func TestShellExecutorReportsSandboxDenialWithoutApprovalCallbackLikeRust(t *testing.T) {
	profile := sandbox.WorkspaceWritePermissionProfile()
	runner := &sequencedShellRunner{results: []*ShellResult{
		sandboxDeniedResult(1, "bash: Permission denied"),
	}}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:    sandbox.ApprovalOnRequest,
			CWD:               t.TempDir(),
			PermissionProfile: &profile,
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-sandbox-denied-headless",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"touch x"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("attempts = %d, want 1", len(runner.requests))
	}
	if !output.SandboxDenied || !strings.Contains(output.Body, "Permission denied") {
		t.Fatalf("output = %#v", output)
	}
}

func TestShellExecutorDoesNotRetrySandboxDenialWhenPolicyNeverLikesRust(t *testing.T) {
	profile := sandbox.WorkspaceWritePermissionProfile()
	runner := &sequencedShellRunner{results: []*ShellResult{
		sandboxDeniedResult(1, "bash: Permission denied"),
	}}
	approvals := 0
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:    sandbox.ApprovalNever,
			CWD:               t.TempDir(),
			PermissionProfile: &profile,
		},
		Approval: func(ctx context.Context, request *ShellApprovalRequest) (ShellApprovalDecision, error) {
			_ = ctx
			_ = request
			approvals++
			return ShellApprovalDecision{Approved: true}, nil
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-sandbox-retry-never",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"touch x"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if approvals != 0 {
		t.Fatalf("approvals = %d, want 0", approvals)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("attempts = %d, want 1", len(runner.requests))
	}
	if !output.SandboxDenied {
		t.Fatalf("output = %#v", output)
	}
}

func TestShellExecutorDoesNotRetryAlreadyEscalatedAttemptLikeRust(t *testing.T) {
	runner := &sequencedShellRunner{results: []*ShellResult{
		sandboxDeniedResult(1, "bash: Permission denied"),
	}}
	approvals := 0
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:         sandbox.ApprovalOnRequest,
			PermissionsPreapproved: true,
			CWD:                    t.TempDir(),
		},
		Approval: func(ctx context.Context, request *ShellApprovalRequest) (ShellApprovalDecision, error) {
			_ = ctx
			_ = request
			approvals++
			return ShellApprovalDecision{Approved: true}, nil
		},
	})

	if _, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-sandbox-retry-escalated",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"touch x","sandbox_permissions":"require_escalated"}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if approvals != 0 {
		t.Fatalf("approvals = %d, want 0", approvals)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("attempts = %d, want 1", len(runner.requests))
	}
}
