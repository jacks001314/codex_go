package execserver

import (
	"os"
	"path/filepath"
	"strings"
)

// Windows sandbox PowerShell fallback resolution.
//
// Rust #49098 (f5430515a8, "Resolve Windows sandbox PowerShell fallbacks on the"
// exec server): a remote controller cannot resolve a sandbox-compatible
// PowerShell executable on the execution host, so the exec server applies the
// existing PowerShell fallback while preparing a request for the Elevated and
// Mxc Windows sandbox backends. pwsh and powershell executable names are
// matched case-insensitively, and an inaccessible Microsoft Store PowerShell
// path (under WindowsApps) is replaced when a compatible executable is
// available; otherwise the requested program is left untouched.
//
// Rust keeps one definition in codex_shell_command::shell_detect and reuses it
// from core and exec-server alike. In Go these predicates live in
// tool/shell_sandbox.go, and tool imports execserver (tool/runner.go,
// tool/shell.go), so exec-server cannot import them without an import cycle.
// The predicates are therefore mirrored here; the #41227 definitions in
// tool/shell_sandbox.go stay authoritative for the local (core) path, and
// moving both into a shared package is a follow-up outside this change.
//
// The mirror is deliberately platform-neutral so that it compiles and is
// testable everywhere; the caller in sandbox_process_windows.go is Windows-only.

// Windows PowerShell fallback paths, mirroring Rust shell_detect.rs
// PWSH_FALLBACK_PATHS / POWERSHELL_FALLBACK_PATHS. Store PowerShell cannot run
// under MXC and can be inaccessible to the elevated sandbox account.
var (
	windowsSandboxPwshFallbackPaths       = []string{`C:\Program Files\PowerShell\7\pwsh.exe`}
	windowsSandboxPowerShellFallbackPaths = []string{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`}
)

func isWindowsPowerShellPathSeparator(r rune) bool {
	return r == '\\' || r == '/'
}

// isInaccessibleWindowsAppsPowerShellPath reports whether path has a component
// following WindowsApps that is a Store PowerShell launcher (pwsh.exe,
// powershell.exe, or a Microsoft.PowerShell* directory). WindowsApps also
// contains valid Codex frameworks, so only those names count.
func isInaccessibleWindowsAppsPowerShellPath(path string) bool {
	parts := strings.FieldsFunc(path, isWindowsPowerShellPathSeparator)
	index := -1
	for i, part := range parts {
		if strings.EqualFold(part, "WindowsApps") {
			index = i
			break
		}
	}
	if index == -1 || index+1 >= len(parts) {
		return false
	}
	next := parts[index+1]
	return strings.EqualFold(next, "pwsh.exe") ||
		strings.EqualFold(next, "powershell.exe") ||
		strings.HasPrefix(strings.ToLower(next), "microsoft.powershell")
}

// targetsInaccessibleWindowsAppsPowerShellPath reports whether path (or its
// canonical target) is a Store PowerShell launcher.
func targetsInaccessibleWindowsAppsPowerShellPath(path string) bool {
	if isInaccessibleWindowsAppsPowerShellPath(path) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return isInaccessibleWindowsAppsPowerShellPath(resolved)
	}
	return false
}

// isWindowsSandboxCompatiblePowerShellPath reports whether path names a real
// .exe PowerShell that is not a Store (WindowsApps) launcher.
func isWindowsSandboxCompatiblePowerShellPath(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".exe") {
		return false
	}
	return !targetsInaccessibleWindowsAppsPowerShellPath(path)
}

func windowsPowerShellFallbackFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// windowsSandboxCompatiblePowerShellPath returns the first compatible
// powershell binary discovered on PATH, then the standard fallback locations.
func windowsSandboxCompatiblePowerShellPath(binaryName string, fallbackPaths []string) (string, bool) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		candidate := filepath.Join(dir, binaryName+".exe")
		if isWindowsSandboxCompatiblePowerShellPath(candidate) && windowsPowerShellFallbackFileExists(candidate) {
			return candidate, true
		}
	}
	for _, candidate := range fallbackPaths {
		if isWindowsSandboxCompatiblePowerShellPath(candidate) && windowsPowerShellFallbackFileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// windowsSandboxPowerShellFallback returns a replacement only when program
// targets Store PowerShell and an unpackaged PowerShell executable can be
// discovered. Mirror of Rust
// shell_detect::fallback_powershell_shell_for_windows_sandbox (#49098, whose
// body generalises the #41227 elevated-only helper).
func windowsSandboxPowerShellFallback(program string) (string, bool) {
	if !targetsInaccessibleWindowsAppsPowerShellPath(program) {
		return "", false
	}
	if replacement, ok := windowsSandboxCompatiblePowerShellPath("pwsh", windowsSandboxPwshFallbackPaths); ok {
		return replacement, true
	}
	if replacement, ok := windowsSandboxCompatiblePowerShellPath("powershell", windowsSandboxPowerShellFallbackPaths); ok {
		return replacement, true
	}
	return "", false
}

// windowsPowerShellProgramStem returns the executable name without directory or
// extension, treating both separators as path separators so that it matches
// Rust Path::file_stem on Windows paths even when running on another platform.
func windowsPowerShellProgramStem(program string) string {
	trimmed := strings.TrimRight(program, `\/`)
	if index := strings.LastIndexAny(trimmed, `\/`); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	if index := strings.LastIndex(trimmed, "."); index > 0 {
		trimmed = trimmed[:index]
	}
	return trimmed
}

// windowsSandboxPowerShellProgram applies the exec-server request-preparation
// fallback from Rust #49098: only the elevated and mxc Windows sandbox backends
// need a sandbox-compatible shell, and only pwsh / powershell program names are
// considered (case-insensitively).
func windowsSandboxPowerShellProgram(program string, level string) string {
	if level != windowsSandboxLevelElevated && level != windowsSandboxLevelMxc {
		return program
	}
	stem := windowsPowerShellProgramStem(program)
	if !strings.EqualFold(stem, "pwsh") && !strings.EqualFold(stem, "powershell") {
		return program
	}
	if replacement, ok := windowsSandboxPowerShellFallback(program); ok {
		return replacement
	}
	return program
}
