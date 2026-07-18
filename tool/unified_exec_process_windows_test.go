//go:build windows

package tool

import (
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
	if got := windowsUnifiedExecSandboxLevel(&profile, sandbox.WindowsSandboxDisabled); got != windowsunified.WindowsSandboxLevelElevated {
		t.Fatalf("deny-read profile level = %q", got)
	}
}
