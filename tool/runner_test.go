package tool

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/execserver"
	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

func TestLocalShellRunnerRequiresRequest(t *testing.T) {
	_, err := NewLocalShellRunner().Run(context.Background(), nil)
	if err == nil {
		t.Fatal("Run returned nil error, want failure")
	}
}

func TestLocalShellRunnerRunsCommand(t *testing.T) {
	shell := &Shell{Type: ShellBash, Path: "/bin/sh"}
	if runtime.GOOS == "windows" {
		shell = &Shell{Type: ShellCmd, Path: "cmd"}
	}
	resolved, err := ResolveCommand(&ExecCommandArgs{Cmd: "echo codex"}, shell, false)
	if err != nil {
		t.Fatalf("ResolveCommand returned error: %v", err)
	}
	result, err := NewLocalShellRunner().Run(context.Background(), &ShellRequest{
		Command: resolved.Command,
		CWD:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, stderr = %q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "codex") {
		t.Fatalf("Stdout = %q", result.Stdout)
	}
}

func TestLocalShellRunnerAppliesCWDAndEnv(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "marker.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	shell := &Shell{Type: ShellBash, Path: "/bin/sh"}
	cmd := `[ "$CODEX_RUNNER_ENV" = "runner" ] && [ -f marker.txt ] && printf ok`
	if runtime.GOOS == "windows" {
		shell = &Shell{Type: ShellCmd, Path: "cmd"}
		cmd = `if "%CODEX_RUNNER_ENV%"=="runner" if exist marker.txt echo ok`
	}
	resolved, err := ResolveCommand(&ExecCommandArgs{Cmd: cmd}, shell, false)
	if err != nil {
		t.Fatalf("ResolveCommand returned error: %v", err)
	}

	result, err := NewLocalShellRunner().Run(context.Background(), &ShellRequest{
		Command: resolved.Command,
		CWD:     cwd,
		Env:     map[string]string{"CODEX_RUNNER_ENV": "runner"},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, stderr = %q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "ok") {
		t.Fatalf("Stdout = %q", result.Stdout)
	}
}

func TestLocalShellRunnerMarksTimeout(t *testing.T) {
	shell := &Shell{Type: ShellBash, Path: "/bin/sh"}
	cmd := "sleep 2"
	if runtime.GOOS == "windows" {
		shell = &Shell{Type: ShellCmd, Path: "cmd"}
		cmd = "ping -n 3 127.0.0.1 >NUL"
	}
	resolved, err := ResolveCommand(&ExecCommandArgs{Cmd: cmd}, shell, false)
	if err != nil {
		t.Fatalf("ResolveCommand returned error: %v", err)
	}

	result, err := NewLocalShellRunner().Run(context.Background(), &ShellRequest{
		Command:   resolved.Command,
		CWD:       t.TempDir(),
		TimeoutMS: 50,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.TimedOut {
		t.Fatalf("TimedOut = false, result = %#v", result)
	}
}

func TestLocalShellRunnerReturnsExitCodeForFailure(t *testing.T) {
	shell := &Shell{Type: ShellBash, Path: "/bin/sh"}
	cmd := "exit 7"
	if runtime.GOOS == "windows" {
		shell = &Shell{Type: ShellCmd, Path: "cmd"}
		cmd = "exit /B 7"
	}
	resolved, err := ResolveCommand(&ExecCommandArgs{Cmd: cmd}, shell, false)
	if err != nil {
		t.Fatalf("ResolveCommand returned error: %v", err)
	}
	result, err := NewLocalShellRunner().Run(context.Background(), &ShellRequest{
		Command: resolved.Command,
		CWD:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d", result.ExitCode)
	}
}

func TestLocalShellRunnerUsesWindowsSandboxForSandboxedProfile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows sandbox capture is Windows-only")
	}
	cwd := t.TempDir()
	codexHome := t.TempDir()
	oldCapture := runWindowsShellSandboxCapture
	oldCodexHome := defaultShellRunnerCodexHome
	called := false
	runWindowsShellSandboxCapture = func(capture *windowssandbox.CaptureRequest, elevated bool) (*windowssandbox.CaptureResult, error) {
		called = true
		if capture == nil {
			t.Fatalf("capture request is nil")
		}
		if elevated {
			t.Fatal("unelevated Windows sandbox request selected elevated backend")
		}
		if capture.PermissionProfile == nil || capture.PermissionProfile.Disabled {
			t.Fatalf("PermissionProfile = %#v, want sandboxed profile", capture.PermissionProfile)
		}
		if capture.PermissionProfileID != ":workspace" {
			t.Fatalf("PermissionProfileID = %q", capture.PermissionProfileID)
		}
		if len(capture.Command) != 3 || capture.Command[0] != "cmd" || capture.Command[2] != "echo sandboxed" {
			t.Fatalf("Command = %#v", capture.Command)
		}
		if capture.CWD != filepath.Clean(cwd) {
			t.Fatalf("CWD = %q, want %q", capture.CWD, filepath.Clean(cwd))
		}
		if len(capture.WorkspaceRoots) != 1 || capture.WorkspaceRoots[0] != filepath.Clean(cwd) {
			t.Fatalf("WorkspaceRoots = %#v", capture.WorkspaceRoots)
		}
		if capture.CodexHome != filepath.Clean(codexHome) {
			t.Fatalf("CodexHome = %q, want %q", capture.CodexHome, filepath.Clean(codexHome))
		}
		if capture.Env["CODEX_PERMISSION_PROFILE"] != ":workspace" || capture.Env["CODEX_RUNNER_ENV"] != "runner" {
			t.Fatalf("Env = %#v", capture.Env)
		}
		if !capture.UsePrivateDesktop || !capture.DisallowSetupElevation {
			t.Fatalf("capture privateDesktop/disallowElevation = %t/%t", capture.UsePrivateDesktop, capture.DisallowSetupElevation)
		}
		return &windowssandbox.CaptureResult{ExitCode: 0, Stdout: []byte("sandboxed")}, nil
	}
	defaultShellRunnerCodexHome = func() string { return codexHome }
	defer func() {
		runWindowsShellSandboxCapture = oldCapture
		defaultShellRunnerCodexHome = oldCodexHome
	}()

	profile := sandbox.WorkspaceWritePermissionProfile()
	result, err := NewLocalShellRunner().Run(context.Background(), &ShellRequest{
		Command:                      []string{"cmd", "/c", "echo sandboxed"},
		CWD:                          cwd,
		Env:                          map[string]string{"CODEX_RUNNER_ENV": "runner"},
		PermissionProfileID:          ":workspace",
		PermissionProfile:            &profile,
		WindowsSandboxLevel:          sandbox.WindowsSandboxUnelevated,
		WindowsSandboxPrivateDesktop: true,
		ApprovalPolicy:               sandbox.ApprovalNever,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !called {
		t.Fatalf("Windows sandbox capture was not called")
	}
	if result.ExitCode != 0 || result.Stdout != "sandboxed" {
		t.Fatalf("result = %#v", result)
	}
}

func TestWindowsShellSandboxUsesElevatedLikeRust(t *testing.T) {
	profile := sandbox.WorkspaceWritePermissionProfile()
	if windowsShellSandboxUsesElevated(&profile, sandbox.WindowsSandboxUnelevated) {
		t.Fatal("ordinary unelevated profile selected elevated backend")
	}
	if !windowsShellSandboxUsesElevated(&profile, sandbox.WindowsSandboxElevated) {
		t.Fatal("explicit elevated profile did not select elevated backend")
	}
	profile.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{}}
	// Rust a603d7ca5c: the backend is selected solely from the configured
	// WindowsSandboxLevel; a deny-read profile no longer forces elevated.
	if windowsShellSandboxUsesElevated(&profile, sandbox.WindowsSandboxUnelevated) {
		t.Fatal("deny-read profile with unelevated level selected elevated backend")
	}
	if !windowsShellSandboxUsesElevated(&profile, sandbox.WindowsSandboxElevated) {
		t.Fatal("deny-read profile with elevated level did not select elevated backend")
	}
}

func TestManagedNetworkingRejectedWithRestrictedTokenSandbox(t *testing.T) {
	cwd := t.TempDir()
	codexHome := t.TempDir()
	oldCapture := runWindowsShellSandboxCapture
	oldCodexHome := defaultShellRunnerCodexHome
	runWindowsShellSandboxCapture = func(capture *windowssandbox.CaptureRequest, elevated bool) (*windowssandbox.CaptureResult, error) {
		t.Fatal("managed networking with a restricted-token sandbox must be rejected before spawning")
		return nil, nil
	}
	defaultShellRunnerCodexHome = func() string { return codexHome }
	defer func() {
		runWindowsShellSandboxCapture = oldCapture
		defaultShellRunnerCodexHome = oldCodexHome
	}()

	profile := sandbox.WorkspaceWritePermissionProfile()
	_, err := NewLocalShellRunner().Run(context.Background(), &ShellRequest{
		Command:               []string{"cmd", "/c", "echo managed"},
		CWD:                   cwd,
		PermissionProfileID:   ":workspace",
		PermissionProfile:     &profile,
		WindowsSandboxLevel:   sandbox.WindowsSandboxUnelevated,
		EnforceManagedNetwork: true,
	})
	if err == nil || !strings.Contains(err.Error(), "managed networking requires the elevated Windows sandbox backend") {
		t.Fatalf("Run error = %v, want managed networking rejection", err)
	}
}

func TestWindowsSandboxProxySettingsMode(t *testing.T) {
	if got := windowsSandboxProxySettingsMode(execserver.WindowsSandboxProxySettingsPreserve); got != windowssandbox.ProxySettingsPreserve {
		t.Fatalf("preserve proxy settings mode = %q", got)
	}
	if got := windowsSandboxProxySettingsMode(execserver.WindowsSandboxProxySettingsReconcile); got != windowssandbox.ProxySettingsReconcile {
		t.Fatalf("reconcile proxy settings mode = %q", got)
	}
}
