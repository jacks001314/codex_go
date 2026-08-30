package tool

import (
	"os"
	"path/filepath"
	"strings"

	"codex_go/sandbox"
)

// Windows PowerShell fallback paths, mirroring Rust shell_detect.rs
// PWSH_FALLBACK_PATHS / POWERSHELL_FALLBACK_PATHS (#41227). The elevated Windows
// sandbox runs as a dedicated account that cannot access Microsoft Store
// PowerShell installed under WindowsApps.
var (
	pwshFallbackPaths       = []string{`C:\Program Files\PowerShell\7\pwsh.exe`}
	powershellFallbackPaths = []string{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`}
)

// isInaccessibleWindowsAppsPowerShellPath reports whether path has a component
// following "WindowsApps" that is a Store PowerShell launcher (pwsh.exe,
// powershell.exe, or a Microsoft.PowerShell* directory).
func isInaccessibleWindowsAppsPowerShellPath(path string) bool {
	parts := strings.FieldsFunc(filepath.Clean(path), func(r rune) bool { return r == '\\' || r == '/' })
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
	lower := strings.ToLower(next)
	return strings.EqualFold(next, "pwsh.exe") ||
		strings.EqualFold(next, "powershell.exe") ||
		strings.HasPrefix(lower, "microsoft.powershell")
}

// targetsInaccessibleWindowsAppsPowerShell reports whether path (or its
// canonical target) is a Store PowerShell launcher.
func targetsInaccessibleWindowsAppsPowerShell(path string) bool {
	if isInaccessibleWindowsAppsPowerShellPath(path) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return isInaccessibleWindowsAppsPowerShellPath(resolved)
	}
	return false
}

// isElevatedSandboxCompatiblePowerShellPath reports whether path names a real
// .exe PowerShell that is not a Store (WindowsApps) launcher.
func isElevatedSandboxCompatiblePowerShellPath(path string) bool {
	if !strings.EqualFold(filepath.Ext(path), ".exe") {
		return false
	}
	return !targetsInaccessibleWindowsAppsPowerShell(path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// elevatedSandboxCompatiblePowerShellPath returns the first compatible
// powershell binary discovered on PATH, then the standard fallback locations.
func elevatedSandboxCompatiblePowerShellPath(binaryName string, fallbackPaths []string) (string, bool) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		candidate := filepath.Join(dir, binaryName+".exe")
		if isElevatedSandboxCompatiblePowerShellPath(candidate) && fileExists(candidate) {
			return candidate, true
		}
	}
	for _, candidate := range fallbackPaths {
		if isElevatedSandboxCompatiblePowerShellPath(candidate) && fileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// fallbackPowerShellShellForElevatedWindowsSandbox returns a replacement shell
// only when shellPath targets a Store (WindowsApps) PowerShell and a compatible
// elevated-sandbox PowerShell can be discovered. Mirror of Rust
// fallback_powershell_shell_for_elevated_windows_sandbox (#41227).
func fallbackPowerShellShellForElevatedWindowsSandbox(shellPath string) *Shell {
	if len(shellPath) == 0 || !targetsInaccessibleWindowsAppsPowerShell(shellPath) {
		return nil
	}
	if replacement, ok := elevatedSandboxCompatiblePowerShellPath("pwsh", pwshFallbackPaths); ok {
		return &Shell{Type: ShellPowerShell, Path: replacement}
	}
	if replacement, ok := elevatedSandboxCompatiblePowerShellPath("powershell", powershellFallbackPaths); ok {
		return &Shell{Type: ShellPowerShell, Path: replacement}
	}
	return nil
}

// preparePowerShellCommandForElevatedWindowsSandbox rewrites a local elevated
// Windows sandbox PowerShell command that targets a Store shell to the first
// compatible pwsh/powershell.exe and ensures -NoProfile is present. Remote and
// non-elevated commands are returned unchanged. Mirror of Rust
// prepare_powershell_command_for_elevated_windows_sandbox (#41227).
func preparePowerShellCommandForElevatedWindowsSandbox(
	command []string,
	shellType ShellType,
	sandboxRequested bool,
	windowsSandboxLevel sandbox.WindowsSandboxLevel,
	environmentIsRemote bool,
) []string {
	if shellType != ShellPowerShell || !sandboxRequested ||
		windowsSandboxLevel != sandbox.WindowsSandboxElevated || environmentIsRemote ||
		len(command) == 0 {
		return command
	}
	command = append([]string(nil), command...)
	if fallback := fallbackPowerShellShellForElevatedWindowsSandbox(command[0]); fallback != nil && fallback.Path != "" {
		command[0] = fallback.Path
	}
	for _, arg := range command[1:] {
		if strings.EqualFold(arg, "-NoProfile") {
			return command
		}
	}
	command = append(command[:1], append([]string{"-NoProfile"}, command[1:]...)...)
	return command
}
