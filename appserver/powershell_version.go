package appserver

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"codex_go/envutil"
)

// powershellVersionCache caches the major.minor version for a resolved
// PowerShell executable path (Rust POWERSHELL_VERSIONS, #41232).
var powershellVersionCache sync.Map

const powershellVersionQueryTimeout = 2 * time.Second

// queryPowerShellVersion resolves a PowerShell executable's major.minor version
// by running a bounded, non-interactive query. Failures cache an empty string so
// the lookup is not retried every turn.
func queryPowerShellVersion(ctx context.Context, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if cached, ok := powershellVersionCache.Load(path); ok {
		return cached.(string)
	}
	queryCtx, cancel := context.WithTimeout(ctx, powershellVersionQueryTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		queryCtx,
		path,
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		"$PSVersionTable.PSVersion.ToString()",
	)
	// Rust #48483: the version probe is piped, so it must not allocate a console
	// window when the app server itself has no console.
	envutil.SuppressConsoleWindow(cmd)
	output, err := cmd.Output()
	version := ""
	if err == nil {
		version = parsePowerShellVersion(string(output))
	}
	powershellVersionCache.Store(path, version)
	return version
}

// parsePowerShellVersion reduces "$PSVersionTable.PSVersion.ToString()" output
// (for example "5.1.19041.1" or "7.4.0") to its major.minor form. Rust #41232
// bounds the probe output to 64 bytes and requires numeric u16 components.
func parsePowerShellVersion(output string) string {
	if len(output) > 64 {
		return ""
	}
	output = strings.TrimSpace(output)
	parts := strings.SplitN(output, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	major, err := strconv.ParseUint(parts[0], 10, 16)
	if err != nil {
		return ""
	}
	minor, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil {
		return ""
	}
	return strconv.FormatUint(major, 10) + "." + strconv.FormatUint(minor, 10)
}
