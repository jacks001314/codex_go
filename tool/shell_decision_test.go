package tool

import (
	"context"
	"testing"

	"codex_go/sandbox"
)

// recordingDecisionSink captures the decisions a tool call received without a
// prompt.
type recordingDecisionSink struct {
	autoApproved []approvedDecision
}

type approvedDecision struct {
	toolName ToolName
	callID   string
}

func (s *recordingDecisionSink) AutoApproved(toolName ToolName, callID string) {
	s.autoApproved = append(s.autoApproved, approvedDecision{toolName: toolName, callID: callID})
}

// A patch that needs no approval reports the same config-approved decision the
// shell executor reports; an approval-gated patch reports nothing (the approval
// path decides).
func TestApplyPatchExecutorReportsAutoApprovedDecisionsLikeRust(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch"

	sink := &recordingDecisionSink{}
	executor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: t.TempDir(), DecisionSink: sink})
	if _, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-3",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: patch},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(sink.autoApproved) != 1 {
		t.Fatalf("decisions = %#v", sink.autoApproved)
	}
	if decision := sink.autoApproved[0]; decision.callID != "call-3" || decision.toolName.Key() != DefaultApplyPatchToolName {
		t.Fatalf("decision = %#v", decision)
	}

	// An approval-gated patch routes through the approval callback instead.
	gated := &recordingDecisionSink{}
	approvals := 0
	gatedExecutor := NewApplyPatchExecutor(&ApplyPatchExecutorOptions{
		CWD:          t.TempDir(),
		DecisionSink: gated,
		Approval: func(context.Context, *ApplyPatchApprovalRequest) (ApplyPatchApprovalDecision, error) {
			approvals++
			return ApplyPatchApprovalDecision{Approved: true}, nil
		},
	})
	if _, err := gatedExecutor.Execute(context.Background(), &Invocation{
		CallID:   "call-4",
		ToolName: PlainName(DefaultApplyPatchToolName),
		Payload:  Payload{Kind: PayloadCustom, Input: patch},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if approvals != 1 || len(gated.autoApproved) != 0 {
		t.Fatalf("approvals = %d decisions = %#v", approvals, gated.autoApproved)
	}
}

// A command that needs no approval reports the config-approved decision Rust's
// orchestrator records for a skipped approval requirement.
func TestShellExecutorReportsAutoApprovedDecisionsLikeRust(t *testing.T) {
	sink := &recordingDecisionSink{}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner:       &fakeShellRunner{result: &ShellResult{}},
		Shell:        &Shell{Type: ShellBash, Path: "/bin/sh"},
		DecisionSink: sink,
		Validation: ShellValidationOptions{
			ApprovalPolicy: sandbox.ApprovalOnRequest,
			CWD:            t.TempDir(),
		},
	})
	if _, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-1",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo hi"}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(sink.autoApproved) != 1 {
		t.Fatalf("decisions = %#v", sink.autoApproved)
	}
	decision := sink.autoApproved[0]
	if decision.callID != "call-1" || decision.toolName.Key() != DefaultExecCommandToolName {
		t.Fatalf("decision = %#v", decision)
	}
}

// A command that asks for escalated permissions needs approval, so the approval
// path decides and no config-approved decision is reported.
func TestShellExecutorSkipsAutoApprovedDecisionWhenApprovalIsRequired(t *testing.T) {
	sink := &recordingDecisionSink{}
	approvals := 0
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner:       &fakeShellRunner{result: &ShellResult{}},
		Shell:        &Shell{Type: ShellBash, Path: "/bin/sh"},
		DecisionSink: sink,
		Approval: func(context.Context, *ShellApprovalRequest) (ShellApprovalDecision, error) {
			approvals++
			return ShellApprovalDecision{Approved: true}, nil
		},
		Validation: ShellValidationOptions{
			ApprovalPolicy: sandbox.ApprovalOnRequest,
			CWD:            t.TempDir(),
		},
	})
	if _, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-2",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"rm -rf build","sandbox_permissions":"require_escalated","justification":"clean"}`},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if approvals != 1 {
		t.Fatalf("approvals = %d", approvals)
	}
	if len(sink.autoApproved) != 0 {
		t.Fatalf("decisions = %#v", sink.autoApproved)
	}
}
