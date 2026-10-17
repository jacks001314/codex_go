package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gofrs/flock"

	"codex_go/install"
	"codex_go/utils"
)

const (
	DispatchApplyPatchArg0           = "apply_patch"
	DispatchMisspelledApplyPatchArg0 = "applypatch"
	DispatchLinuxSandboxArg0         = "codex-linux-sandbox"
	DispatchExecveWrapperArg0        = "codex-execve-wrapper"
	DispatchWindowsSandboxSetupArg0  = "codex-windows-sandbox-setup"
	DispatchWindowsCommandRunnerArg0 = "codex-command-runner"
	DispatchApplyPatchFlag           = "--codex-run-as-apply-patch"
	DispatchWindowsSandboxFlag       = "--run-as-windows-sandbox"
	DispatchWindowsSandboxSetupFlag  = "--codex-run-as-windows-sandbox-setup"
	DispatchWindowsCommandRunnerFlag = "--codex-run-as-windows-command-runner"
	DispatchLockFilename             = ".lock"
	DispatchIllegalEnvVarPrefix      = "CODEX_"
)

type DispatchPaths struct {
	CodexSelfExe                string
	CodexLinuxSandboxExe        string
	MainExecveWrapperExe        string
	CodexWindowsSandboxSetupExe string
	CodexCommandRunnerExe       string
}

type DispatchAliasGuard struct {
	TempDir     string
	UpdatedPATH string
	Paths       DispatchPaths

	// lock is the advisory lock on TempDir/DispatchLockFilename. It is held for
	// as long as the guard is alive so the janitor can tell a live session's
	// directory from a stale one.
	lock *flock.Flock
}

func DispatchKind(argv0 string, argv1 string) string {
	name := utils.CrossPlatformBase(argv0)
	name = dispatchBaseName(name)
	switch name {
	case DispatchApplyPatchArg0, DispatchMisspelledApplyPatchArg0:
		return "apply_patch"
	case DispatchLinuxSandboxArg0:
		return "linux_sandbox"
	case DispatchExecveWrapperArg0:
		return "execve_wrapper"
	case DispatchWindowsSandboxSetupArg0:
		return "windows_sandbox_setup"
	case DispatchWindowsCommandRunnerArg0:
		return "windows_command_runner"
	}
	switch argv1 {
	case DispatchApplyPatchFlag:
		return "apply_patch_core"
	case DispatchWindowsSandboxFlag:
		return "windows_sandbox"
	case DispatchWindowsSandboxSetupFlag:
		return "windows_sandbox_setup"
	case DispatchWindowsCommandRunnerFlag:
		return "windows_command_runner"
	}
	return ""
}

func PrepareArg0Aliases(codexHome string, currentExe string, existingPATH string) (*DispatchAliasGuard, error) {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return nil, fmt.Errorf("codex home is required")
	}
	currentExe = strings.TrimSpace(currentExe)
	if currentExe == "" {
		return nil, fmt.Errorf("current executable is required")
	}
	if abs, err := filepath.Abs(currentExe); err == nil {
		currentExe = abs
	}
	tempRoot := arg0TempRoot(codexHome)
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		return nil, err
	}
	// Best-effort cleanup of stale per-session dirs; failures must not block
	// startup (Rust prints the same warning from prepare_aliases_in_home).
	if err := CleanupStaleDirs(tempRoot); err != nil {
		fmt.Fprintln(os.Stderr, "WARNING: failed to clean up stale arg0 temp dirs:", err)
	}
	tempDir, err := os.MkdirTemp(tempRoot, "codex-arg0")
	if err != nil {
		return nil, err
	}
	cleanupOnError := true
	var lock *flock.Flock
	defer func() {
		if cleanupOnError {
			if lock != nil {
				_ = lock.Unlock()
			}
			_ = os.RemoveAll(tempDir)
		}
	}()
	lockPath := filepath.Join(tempDir, DispatchLockFilename)
	if err := os.WriteFile(lockPath, []byte{}, 0o600); err != nil {
		return nil, err
	}
	// Claim the session directory with an advisory lock held for the life of the
	// guard; the janitor only reclaims directories whose lock is free.
	lock = flock.New(lockPath)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, fmt.Errorf("arg0 temp dir lock is already held: %s", tempDir)
	}
	for _, alias := range dispatchAliasNames(runtime.GOOS) {
		if err := createDispatchAlias(tempDir, alias, currentExe); err != nil {
			return nil, err
		}
	}
	paths := DispatchPaths{CodexSelfExe: currentExe}
	if runtime.GOOS == "linux" {
		paths.CodexLinuxSandboxExe = filepath.Join(tempDir, DispatchLinuxSandboxArg0)
	}
	if runtime.GOOS != "windows" {
		paths.MainExecveWrapperExe = filepath.Join(tempDir, DispatchExecveWrapperArg0)
	} else {
		paths.CodexWindowsSandboxSetupExe = filepath.Join(tempDir, DispatchWindowsSandboxSetupArg0+".bat")
		paths.CodexCommandRunnerExe = filepath.Join(tempDir, DispatchWindowsCommandRunnerArg0+".bat")
	}
	cleanupOnError = false
	return &DispatchAliasGuard{
		TempDir:     tempDir,
		UpdatedPATH: arg0UpdatedPATH(tempDir, currentExe, existingPATH),
		Paths:       paths,
		lock:        lock,
	}, nil
}

// arg0TempRoot isolates helper directories by operating system: Windows and WSL
// can share CODEX_HOME without sharing a lock namespace, so the legacy
// `tmp/arg0` root must not be reused (Rust #53412).
func arg0TempRoot(codexHome string) string {
	return filepath.Join(codexHome, "tmp", "arg0-"+runtime.GOOS)
}

// arg0UpdatedPATH puts the alias directory in front of the PATH the process
// keeps, on top of the package's own `codex-path` directory, mirroring Rust's
// `prepare_path_env_var_with_aliases`: the package path first, then the aliases,
// so a packaged install exposes its bundled command shims to every child while
// the arg0 aliases still win.
func arg0UpdatedPATH(aliasDir string, currentExe string, existingPATH string) string {
	updated := existingPATH
	if packagePathDir := install.PackagePathDirOfExe(currentExe); packagePathDir != "" {
		updated = PathEnvWithEntry(packagePathDir, updated)
	}
	return PathEnvWithEntry(aliasDir, updated)
}

func DispatchPathsForProcess(currentExe string, aliases *DispatchAliasGuard) DispatchPaths {
	currentExe = strings.TrimSpace(currentExe)
	if currentExe != "" {
		if abs, err := filepath.Abs(currentExe); err == nil {
			currentExe = abs
		}
	}
	paths := DispatchPaths{}
	if aliases != nil {
		paths = aliases.Paths
	}
	if strings.TrimSpace(paths.CodexSelfExe) == "" {
		paths.CodexSelfExe = currentExe
	}
	if runtime.GOOS == "linux" && strings.TrimSpace(paths.CodexLinuxSandboxExe) == "" {
		paths.CodexLinuxSandboxExe = currentExe
	}
	return paths
}

func (g *DispatchAliasGuard) Cleanup() error {
	if g == nil || strings.TrimSpace(g.TempDir) == "" {
		return nil
	}
	// Remove the directory only after the lock file is closed so NFS does not
	// retain the open lock as a `.nfs*` entry (Rust #54739).
	err := removeArg0DirLocked(g.TempDir, g.releaseLock)
	if releaseErr := g.releaseLock(); err == nil {
		err = releaseErr
	}
	return err
}

func (g *DispatchAliasGuard) releaseLock() error {
	if g == nil || g.lock == nil {
		return nil
	}
	lock := g.lock
	g.lock = nil
	return lock.Unlock()
}

func PathEnvWithEntry(pathEntry string, existing string) string {
	if existing == "" {
		return pathEntry
	}
	return pathEntry + string(os.PathListSeparator) + existing
}

func LinuxSandboxExePath(paths *DispatchPaths, currentExe string) string {
	if paths != nil && paths.CodexLinuxSandboxExe != "" {
		return paths.CodexLinuxSandboxExe
	}
	if runtime.GOOS == "linux" {
		return currentExe
	}
	return ""
}

func FilterDotenv(values map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range values {
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, DispatchIllegalEnvVarPrefix) || strings.HasPrefix(upper, "GCODE_") {
			continue
		}
		out[key] = value
	}
	return out
}

func CleanupStaleDirs(root string) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		lockPath := filepath.Join(path, DispatchLockFilename)
		if _, err := os.Stat(lockPath); err != nil {
			continue
		}
		// Skip the directory when its lock cannot be taken: the owning process
		// is still running (Rust try_lock_dir).
		lock := flock.New(lockPath)
		locked, err := lock.TryLock()
		if err != nil {
			return err
		}
		if !locked {
			continue
		}
		err = removeArg0DirLocked(path, lock.Unlock)
		_ = lock.Unlock()
		if err != nil {
			if os.IsNotExist(err) {
				// Expected TOCTOU race: directory can disappear after read_dir/lock checks.
				continue
			}
			return err
		}
	}
	return nil
}

func dispatchBaseName(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range []string{".exe", ".bat", ".cmd"} {
		if strings.HasSuffix(lower, ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return name
}

func dispatchAliasNames(goos string) []string {
	if goos == "windows" {
		return []string{
			DispatchApplyPatchArg0 + ".bat",
			DispatchMisspelledApplyPatchArg0 + ".bat",
			DispatchWindowsSandboxSetupArg0 + ".bat",
			DispatchWindowsCommandRunnerArg0 + ".bat",
		}
	}
	names := []string{DispatchApplyPatchArg0, DispatchMisspelledApplyPatchArg0, DispatchExecveWrapperArg0}
	if goos == "linux" {
		names = append(names, DispatchLinuxSandboxArg0)
	}
	return names
}

func createDispatchAlias(dir string, alias string, currentExe string) error {
	target := filepath.Join(dir, alias)
	if runtime.GOOS == "windows" {
		exe := windowsBatchExecutablePath(currentExe, dir)
		quotedExe := `"` + strings.ReplaceAll(exe, `"`, `""`) + `"`
		flag := dispatchFlagForAlias(alias)
		body := fmt.Sprintf("@echo off\r\n%s %s %%*\r\n", quotedExe, flag)
		return os.WriteFile(target, []byte(body), 0o700)
	}
	return os.Symlink(currentExe, target)
}

// windowsBatchExecutablePath returns a path to executable that is safe to embed
// in a generated Windows .bat alias. When executable and aliasDirectory are on
// the same volume it returns a path relative to the batch file using %~dp0, so
// Unicode paths under the alias directory (which the active console code page
// may not represent) still resolve correctly. Otherwise it falls back to the
// absolute path (Rust #40570 windows_batch_executable_path).
func windowsBatchExecutablePath(executable, aliasDirectory string) string {
	if rel, err := filepath.Rel(aliasDirectory, executable); err == nil {
		if !filepath.IsAbs(rel) {
			return `%~dp0` + rel
		}
	}
	return executable
}

func dispatchFlagForAlias(alias string) string {
	switch dispatchBaseName(filepath.Base(alias)) {
	case DispatchApplyPatchArg0, DispatchMisspelledApplyPatchArg0:
		return DispatchApplyPatchFlag
	case DispatchWindowsSandboxSetupArg0:
		return DispatchWindowsSandboxSetupFlag
	case DispatchWindowsCommandRunnerArg0:
		return DispatchWindowsCommandRunnerFlag
	default:
		return DispatchApplyPatchFlag
	}
}
