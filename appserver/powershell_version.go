package appserver

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
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
	output, err := cmd.Output()
	version := ""
	if err == nil {
		version = parsePowerShellVersion(string(output))
	}
	powershellVersionCache.Store(path, version)
	return version
}

// parsePowerShellVersion reduces "$PSVersionTable.PSVersion.ToString()" output
// (for example "5.1.19041.1" or "7.4.0") to its major.minor form.
func parsePowerShellVersion(output string) string {
	output = strings.TrimSpace(output)
	parts := strings.SplitN(output, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[0]) + "." + strings.TrimSpace(parts[1])
}
