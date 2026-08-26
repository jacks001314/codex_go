package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

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
	tempRoot := filepath.Join(codexHome, "tmp", "arg0")
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp(tempRoot, "codex-arg0")
	if err != nil {
		return nil, err
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(tempDir)
		}
	}()
	if err := os.WriteFile(filepath.Join(tempDir, DispatchLockFilename), []byte{}, 0o600); err != nil {
		return nil, err
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
		UpdatedPATH: PathEnvWithEntry(tempDir, existingPATH),
		Paths:       paths,
	}, nil
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
	return os.RemoveAll(g.TempDir)
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
		if _, err := os.Stat(filepath.Join(path, DispatchLockFilename)); err != nil {
			continue
		}
		if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
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
		quotedExe := `"` + strings.ReplaceAll(currentExe, `"`, `""`) + `"`
		flag := dispatchFlagForAlias(alias)
		body := fmt.Sprintf("@echo off\r\n%s %s %%*\r\n", quotedExe, flag)
		return os.WriteFile(target, []byte(body), 0o700)
	}
	return os.Symlink(currentExe, target)
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
