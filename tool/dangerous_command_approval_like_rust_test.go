package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codex_go/sandbox"
)

func dangerousCommandInvocation(t *testing.T, command string) *Invocation {
	t.Helper()
	arguments, err := json.Marshal(map[string]any{"cmd": command})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	return &Invocation{
		CallID:   "call-dangerous-command",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: string(arguments)},
	}
}

// Mirrors Rust #42113's remote Windows regression: a long dangerous PowerShell
// command targeting a Windows executor is rejected with a bounded,
// model-visible message and never reaches the executor.
func TestRemoteWindowsDangerousCommandRejectionIsBoundedLikeRust(t *testing.T) {
	runner := &fakeShellRunner{result: &ShellResult{}}
	windowsShell := &Shell{Type: ShellPowerShell, Path: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner:   runner,
		Shell:    windowsShell,
		OneShot:  true,
		ToolName: PlainName(DefaultExecCommandToolName),
		UnifiedExecEnvironments: []UnifiedExecEnvironment{{
			ID:         "remote-windows",
			CWD:        t.TempDir(),
			Shell:      windowsShell,
			PlatformOS: "windows",
		}},
		Validation: ShellValidationOptions{
			ApprovalPolicy: sandbox.ApprovalNever,
			CWD:            t.TempDir(),
		},
	})

	command := "Remove-Item test -Force; " + strings.Repeat("Write-Output filler; ", 2000)
	output, err := executor.Execute(context.Background(), dangerousCommandInvocation(t, command))
	if output != nil {
		t.Fatalf("output = %#v, want nil for a rejected command", output)
	}
	var callErr *FunctionCallError
	if !errors.As(err, &callErr) || !callErr.RespondsToModel() {
		t.Fatalf("error = %v, want a respond-to-model rejection", err)
	}
	message := callErr.ModelMessage()
	if len(message) >= 1000 {
		t.Fatalf("rejection length = %d, want a bounded message", len(message))
	}
	if !strings.Contains(message, "chars truncated") {
		t.Fatalf("rejection = %q, want a truncation marker", message)
	}
	if !strings.Contains(message, "rejected") {
		t.Fatalf("rejection = %q, want a rejection reason", message)
	}
	if runner.request != nil {
		t.Fatal("a dangerous command must not reach the executor")
	}
}

// A dangerous command always prompts under on-request, even inside a sandbox,
// and approval lets it run.
func TestDangerousCommandRequiresApprovalUnderOnRequestLikeRust(t *testing.T) {
	runner := &fakeShellRunner{result: &ShellResult{}}
	approvals := 0
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner:   runner,
		Shell:    &Shell{Type: ShellBash, Path: "/bin/sh"},
		OneShot:  true,
		ToolName: PlainName(DefaultExecCommandToolName),
		Approval: func(context.Context, *ShellApprovalRequest) (ShellApprovalDecision, error) {
			approvals++
			return ShellApprovalDecision{Approved: true}, nil
		},
		Validation: ShellValidationOptions{
			ApprovalPolicy: sandbox.ApprovalOnRequest,
			CWD:            t.TempDir(),
		},
	})

	if _, err := executor.Execute(context.Background(), dangerousCommandInvocation(t, "rm -rf build")); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if approvals != 1 {
		t.Fatalf("approvals = %d, want one dangerous-command approval", approvals)
	}
	if runner.request == nil {
		t.Fatal("an approved dangerous command should run")
	}

	// Without an approval callback the call reports the approval requirement
	// instead of running.
	unapprovedRunner := &fakeShellRunner{result: &ShellResult{}}
	unapproved := NewShellExecutor(&ShellExecutorOptions{
		Runner:   unapprovedRunner,
		Shell:    &Shell{Type: ShellBash, Path: "/bin/sh"},
		OneShot:  true,
		ToolName: PlainName(DefaultExecCommandToolName),
		Validation: ShellValidationOptions{
			ApprovalPolicy: sandbox.ApprovalOnRequest,
			CWD:            t.TempDir(),
		},
	})
	output, err := unapproved.Execute(context.Background(), dangerousCommandInvocation(t, "rm -rf build"))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || output.Data["approval_required"] != true {
		t.Fatalf("output = %#v, want an approval-required result", output)
	}
	if unapprovedRunner.request != nil {
		t.Fatal("an unapproved dangerous command must not run")
	}
}

// The executor's reported platform decides the command semantics: the same
// cmd.exe words are dangerous for a Windows executor and ordinary for a POSIX
// one, whichever host runs the check.
func TestDangerousCommandUsesExecutorPlatformLikeRust(t *testing.T) {
	run := func(platformOS string) (*Output, *fakeShellRunner) {
		runner := &fakeShellRunner{result: &ShellResult{}}
		shell := &Shell{Type: ShellBash, Path: "/bin/sh"}
		executor := NewShellExecutor(&ShellExecutorOptions{
			Runner:   runner,
			Shell:    shell,
			OneShot:  true,
			ToolName: PlainName(DefaultExecCommandToolName),
			UnifiedExecEnvironments: []UnifiedExecEnvironment{{
				ID:         "env",
				CWD:        t.TempDir(),
				Shell:      shell,
				PlatformOS: platformOS,
			}},
			Validation: ShellValidationOptions{
				ApprovalPolicy: sandbox.ApprovalOnRequest,
				CWD:            t.TempDir(),
			},
		})
		output, err := executor.Execute(context.Background(), dangerousCommandInvocation(t, "cmd /c del /f file.txt"))
		if err != nil {
			t.Fatalf("Execute(%q) error = %v", platformOS, err)
		}
		return output, runner
	}

	windowsOutput, windowsRunner := run("windows")
	if windowsOutput == nil || windowsOutput.Data["approval_required"] != true {
		t.Fatalf("windows output = %#v, want approval required", windowsOutput)
	}
	if windowsRunner.request != nil {
		t.Fatal("a dangerous Windows command must not run without approval")
	}

	posixOutput, posixRunner := run("linux")
	if posixOutput == nil || posixOutput.Data["approval_required"] == true {
		t.Fatalf("posix output = %#v, want no approval requirement", posixOutput)
	}
	if posixRunner.request == nil {
		t.Fatal("a POSIX executor should run the ordinary command")
	}
}
