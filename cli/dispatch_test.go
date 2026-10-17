package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gofrs/flock"
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

// TestWindowsBatchExecutablePathPreservesUnicode pins the #40570 behavior:
// when the executable and the alias directory share a volume, the generated
// .bat alias must reference the executable via a %~dp0-relative path so a
// Unicode path under the alias directory is not corrupted by the active console
// code page; on a different volume it must fall back to the absolute path.
func TestWindowsBatchExecutablePathPreservesUnicode(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows batch-alias path behavior is Windows-specific")
	}

	root := t.TempDir()
	aliasDirectory := filepath.Join(root, "用户", ".codex", "tmp", "arg0")
	executable := filepath.Join(root, "用户", "bin", "cmd.exe")

	// Same volume -> %~dp0-relative path.
	got := windowsBatchExecutablePath(executable, aliasDirectory)
	if !strings.HasPrefix(got, `%~dp0`) {
		t.Fatalf("same-volume path = %q, want %%~dp0-relative", got)
	}
	rel := strings.TrimPrefix(got, `%~dp0`)
	if resolved := filepath.Clean(filepath.Join(aliasDirectory, rel)); resolved != executable {
		t.Fatalf("%%~dp0 path %q resolves to %q, want %q", got, resolved, executable)
	}

	// Different volume -> absolute fallback.
	vol := filepath.VolumeName(aliasDirectory)
	if len(vol) == 0 || vol[0] < 'A' || vol[0] > 'Z' {
		t.Fatalf("unexpected volume name %q", vol)
	}
	otherVol := string(byte('A'+((vol[0]-'A')+1)%26)) + vol[1:]
	differentExe := filepath.Join(otherVol, "bin", "cmd.exe")
	if got := windowsBatchExecutablePath(differentExe, aliasDirectory); got != differentExe {
		t.Fatalf("different-volume path = %q, want absolute %q", got, differentExe)
	}
}

func TestPathEnvWithEntry(t *testing.T) {
	got := PathEnvWithEntry("/new", "/old")
	if !strings.HasPrefix(got, "/new"+string(os.PathListSeparator)) {
		t.Fatalf("unexpected PATH: %q", got)
	}
}

func TestFilterDotenvRejectsCodexKeys(t *testing.T) {
	filtered := FilterDotenv(map[string]string{"CODEX_HOME": "bad", "GCODE_HOME": "bad", "APP_ENV": "ok"})
	if _, ok := filtered["CODEX_HOME"]; ok {
		t.Fatalf("CODEX_HOME was not filtered: %#v", filtered)
	}
	if _, ok := filtered["GCODE_HOME"]; ok {
		t.Fatalf("GCODE_HOME was not filtered: %#v", filtered)
	}
	if filtered["APP_ENV"] != "ok" {
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
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(root) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("cleanup left %d entries, want only the lock-less dir", len(entries))
	}
}

// TestCleanupStaleDirsSkipsHeldLock pins Rust's try_lock_dir behavior: a
// directory whose lock is still held belongs to a live session and must not be
// reclaimed. Once the lock is released the next janitor pass removes it and
// leaves the temp root empty.
func TestCleanupStaleDirsSkipsHeldLock(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "live")
	if err := os.Mkdir(live, 0o700); err != nil {
		t.Fatalf("mkdir live: %v", err)
	}
	lockPath := filepath.Join(live, DispatchLockFilename)
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	if err != nil {
		t.Fatalf("TryLock() error = %v", err)
	}
	if !locked {
		t.Fatalf("TryLock() = false, want true for a fresh lock file")
	}

	if err := CleanupStaleDirs(root); err != nil {
		t.Fatalf("CleanupStaleDirs() error = %v", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("dir with held lock must survive: %v", err)
	}

	if err := lock.Unlock(); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	if err := CleanupStaleDirs(root); err != nil {
		t.Fatalf("CleanupStaleDirs() after unlock error = %v", err)
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatalf("dir should be reclaimed after unlock, stat err = %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(root) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("cleanup left %d entries in the temp root, want 0", len(entries))
	}
}

func TestArg0TempRootIsolatedPerOS(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, "tmp", "arg0-"+runtime.GOOS)
	if got := arg0TempRoot(home); got != want {
		t.Fatalf("arg0TempRoot() = %q, want %q", got, want)
	}
}

// TestPrepareArg0AliasesKeepsLiveSessionDirs covers the combination of the
// startup janitor and the session lock: creating a second guard on the same
// home must not delete the first, still-running session's aliases.
func TestPrepareArg0AliasesKeepsLiveSessionDirs(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "codex")
	if err := os.WriteFile(exe, []byte(""), 0o700); err != nil {
		t.Fatalf("WriteFile(exe) error = %v", err)
	}
	// A stale session directory left behind by a crashed process must be
	// reclaimed by the janitor that runs before the new session dir is created.
	stale := filepath.Join(arg0TempRoot(home), "stale")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatalf("MkdirAll(stale) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, DispatchLockFilename), nil, 0o600); err != nil {
		t.Fatalf("WriteFile(stale lock) error = %v", err)
	}
	first, err := PrepareArg0Aliases(home, exe, "")
	if err != nil {
		t.Fatalf("PrepareArg0Aliases(first) error = %v", err)
	}
	defer first.Cleanup()
	second, err := PrepareArg0Aliases(home, exe, "")
	if err != nil {
		t.Fatalf("PrepareArg0Aliases(second) error = %v", err)
	}
	defer second.Cleanup()
	if first.TempDir == second.TempDir {
		t.Fatalf("both guards share temp dir %q", first.TempDir)
	}
	if filepath.Dir(first.TempDir) != arg0TempRoot(home) {
		t.Fatalf("first temp dir %q is not under %q", first.TempDir, arg0TempRoot(home))
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale dir should be reclaimed by PrepareArg0Aliases, stat err = %v", err)
	}
	for _, alias := range dispatchAliasNames(runtime.GOOS) {
		if _, err := os.Lstat(filepath.Join(first.TempDir, alias)); err != nil {
			t.Fatalf("live session alias %s was removed: %v", alias, err)
		}
	}
}

// TestDispatchAliasGuardHoldsSessionLock asserts the guard owns the advisory
// lock for its lifetime and that Cleanup closes it before removing the dir.
func TestDispatchAliasGuardHoldsSessionLock(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "codex")
	if err := os.WriteFile(exe, []byte(""), 0o700); err != nil {
		t.Fatalf("WriteFile(exe) error = %v", err)
	}
	guard, err := PrepareArg0Aliases(home, exe, "")
	if err != nil {
		t.Fatalf("PrepareArg0Aliases() error = %v", err)
	}
	probe := flock.New(filepath.Join(guard.TempDir, DispatchLockFilename))
	locked, err := probe.TryLock()
	if err != nil {
		t.Fatalf("probe TryLock() error = %v", err)
	}
	if locked {
		_ = probe.Unlock()
		t.Fatalf("guard did not hold the session lock")
	}
	if err := guard.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(guard.TempDir); !os.IsNotExist(err) {
		t.Fatalf("temp dir survived Cleanup, stat err = %v", err)
	}
	entries, err := os.ReadDir(arg0TempRoot(home))
	if err != nil {
		t.Fatalf("ReadDir(arg0TempRoot) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Cleanup left %d entries in the temp root, want 0", len(entries))
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
