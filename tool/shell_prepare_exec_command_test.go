package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
)

func prepareExecInvocation(t *testing.T, arguments string) *Invocation {
	t.Helper()
	return &Invocation{
		CallID:   "exec-review",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: arguments},
	}
}

// Mirrors Rust #58028 `RecordedExecCall::prepare`: a recorded exec_command is
// resolved through the same environment and approval resolution as a live call,
// without executing it.
func TestPrepareExecCommandResolvesLiveApprovalRequirementLikeRust(t *testing.T) {
	cwd := t.TempDir()
	executor := NewShellExecutor(&ShellExecutorOptions{
		Shell:      &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{CWD: cwd, ApprovalPolicy: sandbox.ApprovalOnRequest},
	})

	prepared, err := executor.PrepareExecCommand(prepareExecInvocation(t, `{"cmd":"touch guardian-direct.txt"}`))
	if err != nil {
		t.Fatalf("PrepareExecCommand() error = %v", err)
	}
	if prepared.Request == nil {
		t.Fatal("PrepareExecCommand() returned no request")
	}
	if prepared.Request.CWD != cwd {
		t.Fatalf("CWD = %q, want %q", prepared.Request.CWD, cwd)
	}
	if prepared.Request.ApprovalRequired {
		t.Fatal("an ordinary command under on-request must not require approval")
	}
	// Preparation must not run the command.
	if _, err := os.Stat(filepath.Join(cwd, "guardian-direct.txt")); !os.IsNotExist(err) {
		t.Fatalf("PrepareExecCommand executed the command: %v", err)
	}

	// Escalation keeps the live approval requirement and reason.
	escalated, err := executor.PrepareExecCommand(prepareExecInvocation(t, `{"cmd":"touch guardian-direct.txt","sandbox_permissions":"require_escalated","justification":"Create the requested file."}`))
	if err != nil {
		t.Fatalf("PrepareExecCommand(escalated) error = %v", err)
	}
	if !escalated.Request.ApprovalRequired {
		t.Fatal("an escalated command must require approval")
	}
	if !strings.Contains(escalated.Request.ApprovalReason, "Create the requested file.") {
		t.Fatalf("approval reason = %q", escalated.Request.ApprovalReason)
	}
}

func TestPrepareExecCommandResolvesRecordedEnvironmentLikeRust(t *testing.T) {
	cwd := t.TempDir()
	environmentCWD := t.TempDir()
	executor := NewShellExecutor(&ShellExecutorOptions{
		Shell:      &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{CWD: cwd, ApprovalPolicy: sandbox.ApprovalOnRequest},
		UnifiedExecEnvironments: []UnifiedExecEnvironment{{
			ID:    "env-1",
			CWD:   environmentCWD,
			Shell: &Shell{Type: ShellBash, Path: "/bin/sh"},
		}},
	})
	prepared, err := executor.PrepareExecCommand(prepareExecInvocation(t, `{"cmd":"ls","environment_id":"env-1"}`))
	if err != nil {
		t.Fatalf("PrepareExecCommand() error = %v", err)
	}
	if prepared.Request.UnifiedExecEnvironmentID != "env-1" {
		t.Fatalf("environment id = %q, want env-1", prepared.Request.UnifiedExecEnvironmentID)
	}
	if prepared.Request.CWD != environmentCWD {
		t.Fatalf("CWD = %q, want the environment cwd %q", prepared.Request.CWD, environmentCWD)
	}

	// An unknown environment is rejected exactly as a live call rejects it.
	_, err = executor.PrepareExecCommand(prepareExecInvocation(t, `{"cmd":"ls","environment_id":"missing"}`))
	if err == nil {
		t.Fatal("an unknown environment must be rejected")
	}
}

func TestPrepareExecCommandRejectsEscalationUnderNeverLikeRust(t *testing.T) {
	executor := NewShellExecutor(&ShellExecutorOptions{
		Shell:      &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{CWD: t.TempDir(), ApprovalPolicy: sandbox.ApprovalNever},
	})
	_, err := executor.PrepareExecCommand(prepareExecInvocation(t, `{"cmd":"ls","sandbox_permissions":"require_escalated"}`))
	if err == nil || !strings.Contains(err.Error(), "approval policy is never") {
		t.Fatalf("PrepareExecCommand() error = %v, want the policy rejection", err)
	}
}

func TestPrepareExecCommandKeepsArgumentsJSONLikeRust(t *testing.T) {
	executor := NewShellExecutor(&ShellExecutorOptions{
		Shell:      &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{CWD: t.TempDir(), ApprovalPolicy: sandbox.ApprovalOnRequest},
	})
	arguments := `{"cmd":"echo ok","timeout_ms":2500,"env":{"A":"1"}}`
	prepared, err := executor.PrepareExecCommand(prepareExecInvocation(t, arguments))
	if err != nil {
		t.Fatalf("PrepareExecCommand() error = %v", err)
	}
	if prepared.Args.TimeoutMS != 2500 || prepared.Args.Env["A"] != "1" {
		data, _ := json.Marshal(prepared.Args)
		t.Fatalf("args = %s", data)
	}
}
