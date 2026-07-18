package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDispatchKind(t *testing.T) {
	if DispatchKind("/tmp/apply_patch", "") != "apply_patch" {
		t.Fatalf("apply_patch dispatch failed")
	}
	if DispatchKind(`C:\tools\apply_patch.exe`, "") != "apply_patch" {
		t.Fatalf("apply_patch extension dispatch failed")
	}
	if DispatchKind("codex", "--codex-run-as-apply-patch") != "apply_patch_core" {
		t.Fatalf("argv1 dispatch failed")
	}
	if DispatchKind("/tmp/codex-linux-sandbox", "") != "linux_sandbox" {
		t.Fatalf("linux sandbox dispatch failed")
	}
	if DispatchKind("/tmp/codex-execve-wrapper", "") != "execve_wrapper" {
		t.Fatalf("execve wrapper dispatch failed")
	}
	if DispatchKind(`C:\tools\codex-windows-sandbox-setup.bat`, "") != "windows_sandbox_setup" {
		t.Fatalf("windows sandbox setup dispatch failed")
	}
	if DispatchKind(`C:\tools\codex-command-runner.exe`, "") != "windows_command_runner" {
		t.Fatalf("windows command runner dispatch failed")
	}
	if DispatchKind("codex.exe", DispatchWindowsSandboxSetupFlag) != "windows_sandbox_setup" {
		t.Fatalf("windows sandbox setup flag dispatch failed")
	}
	if DispatchKind("codex.exe", DispatchWindowsSandboxFlag) != "windows_sandbox" {
		t.Fatalf("windows sandbox wrapper flag dispatch failed")
	}
	if DispatchKind("codex.exe", DispatchWindowsCommandRunnerFlag) != "windows_command_runner" {
		t.Fatalf("windows command runner flag dispatch failed")
	}
}

func TestPathEnvWithEntry(t *testing.T) {
	got := PathEnvWithEntry("/new", "/old")
	if !strings.HasPrefix(got, "/new"+string(os.PathListSeparator)) {
		t.Fatalf("unexpected PATH: %q", got)
	}
}

func TestFilterDotenvRejectsCodexKeys(t *testing.T) {
	filtered := FilterDotenv(map[string]string{"CODEX_HOME": "bad", "APP_ENV": "ok"})
	if _, ok := filtered["CODEX_HOME"]; ok || filtered["APP_ENV"] != "ok" {
		t.Fatalf("unexpected filtered env: %#v", filtered)
	}
}

func TestCleanupStaleDirs(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "stale")
	active := filepath.Join(root, "active")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatalf("mkdir stale: %v", err)
	}
	if err := os.Mkdir(active, 0o700); err != nil {
		t.Fatalf("mkdir active: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, DispatchLockFilename), []byte(""), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if err := CleanupStaleDirs(root); err != nil {
		t.Fatalf("CleanupStaleDirs() error = %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale dir should be removed")
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active dir should remain: %v", err)
	}
}

func TestPrepareArg0Aliases(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "codex")
	if err := os.WriteFile(exe, []byte(""), 0o700); err != nil {
		t.Fatalf("WriteFile(exe) error = %v", err)
	}
	guard, err := PrepareArg0Aliases(home, exe, "/old")
	if err != nil {
		t.Fatalf("PrepareArg0Aliases() error = %v", err)
	}
	defer guard.Cleanup()
	if !strings.HasPrefix(guard.UpdatedPATH, guard.TempDir+string(os.PathListSeparator)) {
		t.Fatalf("UpdatedPATH = %q, want temp dir first", guard.UpdatedPATH)
	}
	if guard.Paths.CodexSelfExe == "" {
		t.Fatalf("CodexSelfExe is empty")
	}
	for _, alias := range dispatchAliasNames(runtime.GOOS) {
		if _, err := os.Lstat(filepath.Join(guard.TempDir, alias)); err != nil {
			t.Fatalf("alias %s missing: %v", alias, err)
		}
	}
	if runtime.GOOS == "linux" && guard.Paths.CodexLinuxSandboxExe == "" {
		t.Fatalf("CodexLinuxSandboxExe is empty on linux")
	}
	if runtime.GOOS != "windows" && guard.Paths.MainExecveWrapperExe == "" {
		t.Fatalf("MainExecveWrapperExe is empty on unix")
	}
	if runtime.GOOS == "windows" && guard.Paths.CodexWindowsSandboxSetupExe == "" {
		t.Fatalf("CodexWindowsSandboxSetupExe is empty on windows")
	}
	if runtime.GOOS == "windows" && guard.Paths.CodexCommandRunnerExe == "" {
		t.Fatalf("CodexCommandRunnerExe is empty on windows")
	}
}

func TestDispatchPathsForProcessUsesAliasesAndLinuxFallback(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "codex")
	if err := os.WriteFile(exe, []byte(""), 0o700); err != nil {
		t.Fatalf("WriteFile(exe) error = %v", err)
	}
	guard, err := PrepareArg0Aliases(home, exe, "")
	if err != nil {
		t.Fatalf("PrepareArg0Aliases() error = %v", err)
	}
	defer guard.Cleanup()

	withAliases := DispatchPathsForProcess(exe, guard)
	if withAliases.CodexSelfExe == "" {
		t.Fatalf("CodexSelfExe is empty")
	}
	if runtime.GOOS == "linux" && withAliases.CodexLinuxSandboxExe != guard.Paths.CodexLinuxSandboxExe {
		t.Fatalf("CodexLinuxSandboxExe = %q, want alias %q", withAliases.CodexLinuxSandboxExe, guard.Paths.CodexLinuxSandboxExe)
	}
	if runtime.GOOS != "windows" && withAliases.MainExecveWrapperExe != guard.Paths.MainExecveWrapperExe {
		t.Fatalf("MainExecveWrapperExe = %q, want alias %q", withAliases.MainExecveWrapperExe, guard.Paths.MainExecveWrapperExe)
	}

	withoutAliases := DispatchPathsForProcess(exe, nil)
	if withoutAliases.CodexSelfExe == "" {
		t.Fatalf("CodexSelfExe without aliases is empty")
	}
	if runtime.GOOS == "linux" && withoutAliases.CodexLinuxSandboxExe != withoutAliases.CodexSelfExe {
		t.Fatalf("linux fallback helper = %q, want current exe %q", withoutAliases.CodexLinuxSandboxExe, withoutAliases.CodexSelfExe)
	}
	if runtime.GOOS != "windows" && withoutAliases.MainExecveWrapperExe != "" {
		t.Fatalf("execve wrapper should not fallback without aliases: %q", withoutAliases.MainExecveWrapperExe)
	}
}
