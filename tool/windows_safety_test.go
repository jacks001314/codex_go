package tool

import (
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
)

func TestBuildShellRequestRejectsPowerShellPipelineToCmdDestructiveOperation(t *testing.T) {
	_, err := BuildShellRequest(&ExecCommandArgs{
		Cmd: `Get-ChildItem src -Recurse | cmd /c del`,
	}, &Shell{Type: ShellPowerShell, Path: "powershell"}, ShellValidationOptions{
		ApprovalPolicy: sandbox.ApprovalOnRequest,
		CWD:            t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "cmd /c") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildShellRequestRejectsCmdComposedWithPowerShellDestructiveOperation(t *testing.T) {
	_, err := BuildShellRequest(&ExecCommandArgs{
		Cmd: `powershell -NoProfile -Command "Get-ChildItem src -Recurse" && del src\file.txt`,
	}, &Shell{Type: ShellCmd, Path: "cmd"}, ShellValidationOptions{
		ApprovalPolicy: sandbox.ApprovalOnRequest,
		CWD:            t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "PowerShell enumeration") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildShellRequestRejectsPowerShellStartProcessWithoutHiddenWindow(t *testing.T) {
	_, err := BuildShellRequest(&ExecCommandArgs{
		Cmd: `Start-Process notepad.exe`,
	}, &Shell{Type: ShellPowerShell, Path: "pwsh"}, ShellValidationOptions{
		ApprovalPolicy: sandbox.ApprovalOnRequest,
		CWD:            t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "WindowStyle Hidden") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildShellRequestAllowsPowerShellStartProcessWithHiddenWindow(t *testing.T) {
	_, err := BuildShellRequest(&ExecCommandArgs{
		Cmd: `Start-Process pwsh -WindowStyle Hidden -ArgumentList "-NoProfile"`,
	}, &Shell{Type: ShellPowerShell, Path: "pwsh"}, ShellValidationOptions{
		ApprovalPolicy: sandbox.ApprovalOnRequest,
		CWD:            t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildShellRequest returned error: %v", err)
	}
}

func TestBuildShellRequestRejectsRecursivePowerShellOperationWithoutLiteralPath(t *testing.T) {
	_, err := BuildShellRequest(&ExecCommandArgs{
		Cmd: `Remove-Item -Path tmp -Recurse -Force`,
	}, &Shell{Type: ShellPowerShell, Path: "powershell"}, ShellValidationOptions{
		ApprovalPolicy: sandbox.ApprovalOnRequest,
		CWD:            t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "LiteralPath") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildShellRequestAllowsRecursivePowerShellOperationInsideCWD(t *testing.T) {
	cwd := t.TempDir()
	_, err := BuildShellRequest(&ExecCommandArgs{
		Cmd: `Remove-Item -LiteralPath tmp -Recurse -Force`,
	}, &Shell{Type: ShellPowerShell, Path: "powershell"}, ShellValidationOptions{
		ApprovalPolicy: sandbox.ApprovalOnRequest,
		CWD:            cwd,
	})
	if err != nil {
		t.Fatalf("BuildShellRequest returned error: %v", err)
	}
}

func TestBuildShellRequestRejectsRecursivePowerShellOperationOutsideCWD(t *testing.T) {
	cwd := t.TempDir()
	outside := filepath.Join(filepath.Dir(cwd), "outside")
	_, err := BuildShellRequest(&ExecCommandArgs{
		Cmd: `Remove-Item -LiteralPath "` + outside + `" -Recurse -Force`,
	}, &Shell{Type: ShellPowerShell, Path: "powershell"}, ShellValidationOptions{
		ApprovalPolicy: sandbox.ApprovalOnRequest,
		CWD:            cwd,
	})
	if err == nil || !strings.Contains(err.Error(), "current workspace") {
		t.Fatalf("error = %v", err)
	}
}
