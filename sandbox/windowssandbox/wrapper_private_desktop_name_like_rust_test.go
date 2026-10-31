package windowssandbox

import (
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
)

const testPrivateDesktopName = "CodexSandboxDesktop-0123456789abcdef0123456789abcdef"

// TestWindowsSandboxWrapperCarriesPrivateDesktopNameLikeRust pins Rust #44658:
// the caller selects the desktop and the wrapper command line carries its name
// so a short-lived wrapper never owns the desktop it runs on.
func TestWindowsSandboxWrapperCarriesPrivateDesktopNameLikeRust(t *testing.T) {
	profile := coresandbox.ReadOnlyPermissionProfile()
	args, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
		Command:             []string{"cmd", "/c", "echo hi"},
		CommandCWD:          `C:\repo`,
		WorkspaceRoots:      []string{`C:\repo`},
		Env:                 map[string]string{},
		PermissionProfile:   &profile,
		WindowsSandboxLevel: WindowsSandboxLevelRestrictedToken,
		CodexHome:           `C:\Users\codex\.gcode`,
		PrivateDesktopName:  testPrivateDesktopName,
	})
	if err != nil {
		t.Fatalf("CreateWindowsSandboxCommandArgsForPermissionProfile() error = %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), privateDesktopNameFlag+" "+testPrivateDesktopName) {
		t.Fatalf("args = %#v, want %s %s", args, privateDesktopNameFlag, testPrivateDesktopName)
	}
	parsed, err := ParseWindowsSandboxWrapperArgs(args)
	if err != nil {
		t.Fatalf("ParseWindowsSandboxWrapperArgs() error = %v", err)
	}
	if parsed.PrivateDesktopName != testPrivateDesktopName {
		t.Fatalf("parsed private desktop = %q, want %q", parsed.PrivateDesktopName, testPrivateDesktopName)
	}
	if capture := wrapperCaptureRequest(parsed); capture.PrivateDesktopName != testPrivateDesktopName {
		t.Fatalf("capture private desktop = %q, want %q", capture.PrivateDesktopName, testPrivateDesktopName)
	}
}

func TestWindowsSandboxWrapperRejectsInvalidPrivateDesktopNameLikeRust(t *testing.T) {
	profile := coresandbox.ReadOnlyPermissionProfile()
	_, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
		Command:             []string{"cmd"},
		CommandCWD:          `C:\repo`,
		Env:                 map[string]string{},
		PermissionProfile:   &profile,
		WindowsSandboxLevel: WindowsSandboxLevelRestrictedToken,
		CodexHome:           `C:\Users\codex\.gcode`,
		PrivateDesktopName:  `Winsta0\Default`,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid private desktop name") {
		t.Fatalf("error = %v, want invalid private desktop name", err)
	}
}

