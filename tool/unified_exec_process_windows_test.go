//go:build windows

package tool

import (
	"strings"
	"testing"

	"codex_go/sandbox"
	windowsunified "codex_go/sandbox/windowssandbox/unified_exec"
)

func TestWindowsUnifiedExecSandboxLevelUsesElevatedForDenyReadLikeRust(t *testing.T) {
	profile := sandbox.WorkspaceWritePermissionProfile()
	if got := windowsUnifiedExecSandboxLevel(&profile, sandbox.WindowsSandboxDisabled); got != windowsunified.WindowsSandboxLevelLegacy {
		t.Fatalf("ordinary profile level = %q", got)
	}
	if got := windowsUnifiedExecSandboxLevel(&profile, sandbox.WindowsSandboxElevated); got != windowsunified.WindowsSandboxLevelElevated {
		t.Fatalf("configured elevated profile level = %q", got)
	}
	profile.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{}}
	// Rust a603d7ca5c: the backend is selected solely from the configured
	// WindowsSandboxLevel; a deny-read profile no longer forces elevated.
	if got := windowsUnifiedExecSandboxLevel(&profile, sandbox.WindowsSandboxDisabled); got != windowsunified.WindowsSandboxLevelLegacy {
		t.Fatalf("deny-read profile with disabled level = %q, want legacy", got)
	}
	if got := windowsUnifiedExecSandboxLevel(&profile, sandbox.WindowsSandboxElevated); got != windowsunified.WindowsSandboxLevelElevated {
		t.Fatalf("deny-read profile with elevated level = %q", got)
	}
}

// TestUnifiedExecWindowsSandboxRejectsMxcLikeRust mirrors Rust #46271: the
// native MXC backend must not silently degrade to the legacy unified-exec
// sandbox when it is unavailable.
func TestUnifiedExecWindowsSandboxRejectsMxcLikeRust(t *testing.T) {
	profile := sandbox.WorkspaceWritePermissionProfile()
	_, err := startUnifiedExecWindowsSandboxCommand(&ShellRequest{
		Command:             []string{"cmd", "/c", "echo"},
		CWD:                 t.TempDir(),
		PermissionProfile:   &profile,
		WindowsSandboxLevel: sandbox.WindowsSandboxMxc,
	})
	if err == nil || !strings.Contains(err.Error(), "native MXC is unavailable on this executor") {
		t.Fatalf("startUnifiedExecWindowsSandboxCommand(mxc) error = %v, want the native MXC error", err)
	}
}
