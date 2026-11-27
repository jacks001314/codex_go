package appserver

import (
	"context"
	"errors"
	osexec "os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestPowerShellHookScriptNamedBlockDetectionLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   bool
	}{
		{name: "end block", script: "end { exit 2 }", want: true},
		{name: "leading line comment", script: "# hook comment\nend { exit 2 }", want: true},
		{name: "leading block comment", script: "<# hook comment #>\nprocess { '{}' }", want: true},
		{name: "param declaration", script: "param([string]$Name)\nbegin { $Name }", want: true},
		{name: "typed declaration", script: "[CmdletBinding()] param() clean { }", want: true},
		{name: "native argument", script: "cmd.exe /c 'echo end && exit /b 0'", want: false},
		{name: "ordinary script block", script: "& { 'result' }", want: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := powershellHookScriptHasNamedBlock(testCase.script); got != testCase.want {
				t.Fatalf("powershellHookScriptHasNamedBlock(%q) = %v, want %v", testCase.script, got, testCase.want)
			}
		})
	}
}

func TestHookRunnerPowerShellPreservesNativeExitCodeLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell hook launcher is Windows-specific")
	}
	powershell, err := osexec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("powershell.exe is unavailable")
	}
	tests := []struct {
		name       string
		command    string
		wantCode   int
		wantStderr string
	}{
		{name: "native command", command: `cmd.exe /c "echo patch denied 1>&2 & exit /b 2"`, wantCode: 2, wantStderr: "patch denied"},
		{name: "named end block", command: `end { [Console]::Error.WriteLine('patch denied'); exit 2 }`, wantCode: 2, wantStderr: "patch denied"},
		{name: "successful native command", command: `cmd.exe /c "exit /b 0"`, wantCode: 0},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			command := testCase.command
			runner := &HookRunner{
				ShellProgram: powershell,
				ShellArgs:    []string{"-NoProfile", "-NonInteractive", "-Command"},
			}
			cmd := runner.commandForHook(context.Background(), HookMetadata{Command: &command})
			output, err := cmd.CombinedOutput()
			exitCode := 0
			if err != nil {
				var exitErr *osexec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("CombinedOutput() error = %v, output = %q", err, output)
				}
				exitCode = exitErr.ExitCode()
			}
			if exitCode != testCase.wantCode {
				t.Fatalf("exit code = %d, want %d; output = %q", exitCode, testCase.wantCode, output)
			}
			if testCase.wantStderr != "" && !strings.Contains(string(output), testCase.wantStderr) {
				t.Fatalf("output = %q, want stderr containing %q", output, testCase.wantStderr)
			}
		})
	}
}
