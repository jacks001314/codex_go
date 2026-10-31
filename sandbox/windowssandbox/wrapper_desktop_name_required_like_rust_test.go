package windowssandbox

import (
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
)

// Mirrors Rust #44658 parse_windows_sandbox_wrapper_args: a sandboxed wrapper
// invocation must carry the caller-owned desktop name, so a short-lived wrapper
// can never create (and immediately lose) its own desktop. Go keeps the
// disabled level exempt because it runs the command without a desktop.
func TestWindowsSandboxWrapperRequiresPrivateDesktopNameLikeRust(t *testing.T) {
	profile := coresandbox.ReadOnlyPermissionProfile()
	args, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
		Command:             []string{"cmd"},
		CommandCWD:          `C:\repo`,
		Env:                 map[string]string{},
		PermissionProfile:   &profile,
		WindowsSandboxLevel: WindowsSandboxLevelRestrictedToken,
		CodexHome:           `C:\Users\codex\.gcode`,
	})
	if err != nil {
		t.Fatalf("CreateWindowsSandboxCommandArgsForPermissionProfile() error = %v", err)
	}
	if strings.Contains(strings.Join(args, " "), privateDesktopNameFlag) {
		t.Fatalf("args unexpectedly carry a desktop name: %#v", args)
	}
	if _, err := ParseWindowsSandboxWrapperArgs(args); err == nil || !strings.Contains(err.Error(), "missing required "+privateDesktopNameFlag) {
		t.Fatalf("parse error = %v, want missing required %s", err, privateDesktopNameFlag)
	}

	disabled, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
		Command:             []string{"cmd"},
		CommandCWD:          `C:\repo`,
		Env:                 map[string]string{},
		PermissionProfile:   &profile,
		WindowsSandboxLevel: WindowsSandboxLevelDisabled,
		CodexHome:           `C:\Users\codex\.gcode`,
	})
	if err != nil {
		t.Fatalf("disabled CreateWindowsSandboxCommandArgsForPermissionProfile() error = %v", err)
	}
	if _, err := ParseWindowsSandboxWrapperArgs(disabled); err != nil {
		t.Fatalf("disabled parse error = %v, want no desktop requirement", err)
	}
}
