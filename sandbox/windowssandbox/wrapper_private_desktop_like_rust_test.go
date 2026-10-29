package windowssandbox

import (
	"testing"

	coresandbox "codex_go/sandbox"
)

// TestWindowsSandboxWrapperAlwaysPassesPrivateDesktopLikeRust pins Rust #46554:
// the private-desktop opt-out is gone, so every sandbox command line carries the
// private-desktop marker regardless of the sandbox level.
func TestWindowsSandboxWrapperAlwaysPassesPrivateDesktopLikeRust(t *testing.T) {
	profile := coresandbox.WorkspaceWritePermissionProfile()
	for _, level := range []WindowsSandboxLevel{WindowsSandboxLevelRestrictedToken, WindowsSandboxLevelElevated} {
		args, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
			Command:             []string{"cmd", "/c", "echo hi"},
			CommandCWD:          `C:\repo`,
			WorkspaceRoots:      []string{`C:\repo`},
			Env:                 map[string]string{"TEMP": `C:\tmp`},
			PermissionProfile:   &profile,
			WindowsSandboxLevel: level,
			CodexHome:           `C:\Users\codex\.gcode`,
		})
		if err != nil {
			t.Fatalf("CreateWindowsSandboxCommandArgsForPermissionProfile(%q) error = %v", level, err)
		}
		found := false
		for _, arg := range args {
			if arg == privateDesktopFlag {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("level %q args = %#v, want %s", level, args, privateDesktopFlag)
		}
	}
}
