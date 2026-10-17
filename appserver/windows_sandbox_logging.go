package appserver

import (
	"os"
	"runtime"
	"strings"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

// windowsSandboxBackendLabel mirrors Rust #51974's selection labels: the native
// MXC backend is reported separately from the legacy sandbox levels.
func windowsSandboxBackendLabel(level sandbox.WindowsSandboxLevel) string {
	switch level {
	case sandbox.WindowsSandboxMxc:
		return "mxc"
	case sandbox.WindowsSandboxElevated:
		return "elevated"
	case sandbox.WindowsSandboxUnelevated:
		return "unelevated"
	default:
		return "disabled"
	}
}

// logWindowsSandboxSelection records the selected Windows sandbox backend in
// the sandbox log directory for this Codex home (Rust #51974
// log_windows_sandbox_selection). Logging is best effort and never fails
// startup; non-Windows hosts have no sandbox selection to record.
func logWindowsSandboxSelection(codexHome string, level sandbox.WindowsSandboxLevel) {
	if runtime.GOOS != "windows" {
		return
	}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return
	}
	dir := windowssandbox.SandboxDir(codexHome)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = windowssandbox.LogNoteInDir(dir, "Windows sandbox selection: "+windowsSandboxBackendLabel(level))
}

// logWindowsSandboxSelectionForConfig resolves the effective level from the
// config service and records it. A missing or unreadable config leaves the
// selection at "disabled", matching Rust's default level.
func logWindowsSandboxSelectionForConfig(configService *config.ConfigService) {
	if configService == nil {
		return
	}
	level := sandbox.WindowsSandboxDisabled
	if read, err := configService.Read(&config.ConfigReadParams{}); err == nil && read != nil && read.Config != nil {
		level = windowsSandboxLevelFromConfigValues(read.Config)
	}
	logWindowsSandboxSelection(configService.CodexHome(), level)
}
