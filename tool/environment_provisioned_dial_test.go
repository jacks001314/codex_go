package tool

import (
	"context"
	"testing"
	"time"

	"codex_go/execserver"
	"codex_go/sandbox"
)

// Rust #48575 (`985cf47a4e`): a provisioned environment's executor may still be
// resuming after a ready report, so an initial connection to it keeps the fixed
// five-minute window; every other environment keeps the caller's ordinary
// budget. Go stores that fact on the environment (`UnifiedExecEnvironment
// .Provisioned`) so the unified-exec, environment-filesystem and remote-skills
// dial sites all agree, mirroring Rust's single Deferred transport.
func TestProvisionedEnvironmentConnectBudgetLikeRust(t *testing.T) {
	provisioned := UnifiedExecEnvironment{ID: "tools", Provisioned: true}
	if got, want := provisioned.ConnectBudget(10*time.Second), execserver.ProvisionedEnvironmentConnectTimeout(); got != want {
		t.Fatalf("provisioned connect budget = %v, want the five-minute window %v", got, want)
	}
	ordinary := UnifiedExecEnvironment{ID: "plain"}
	if got := ordinary.ConnectBudget(10 * time.Second); got != 10*time.Second {
		t.Fatalf("ordinary connect budget = %v, want the caller's budget verbatim", got)
	}
}

// TestShellExecutorMarksProvisionedEnvironmentRequestsLikeRust verifies the
// production wiring: selecting a provisioned environment marks the shell
// request, which is what tool/execRemote hands to the initial Noise dial as
// `Provisioned` (Rust #48575).
func TestShellExecutorMarksProvisionedEnvironmentRequestsLikeRust(t *testing.T) {
	cwd := t.TempDir()
	runner := &fakeShellRunner{result: &ShellResult{ExitCode: 0}}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			ApprovalPolicy:   sandbox.ApprovalOnRequest,
			CWD:              cwd,
			DefaultTimeoutMS: 5000,
		},
		UnifiedExecEnvironments: []UnifiedExecEnvironment{
			{ID: "tools", CWD: cwd, Provisioned: true},
		},
	})

	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-provisioned-environment",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"echo hi","environment_id":"tools"}`},
	})
	if err != nil {
		t.Fatalf("Execute(tools) error = %v", err)
	}
	if !output.Success {
		t.Fatalf("Execute(tools) failed: %#v", output)
	}
	if runner.request == nil || runner.request.UnifiedExecEnvironmentID != "tools" {
		t.Fatalf("request environment = %#v", runner.request)
	}
	if !runner.request.UnifiedExecProvisioned {
		t.Fatal("selected provisioned environment did not mark the shell request as provisioned")
	}
}
