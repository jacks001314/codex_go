package windowssandbox

import (
	"testing"

	coresandbox "codex_go/sandbox"
)

func TestPermissionProfileWorkspaceWriteUsesWindowsTempEnvVars(t *testing.T) {
	profile := coresandbox.WorkspaceWritePermissionProfile()
	mode, err := TokenModeForPermissionProfile(&profile, nil, `C:\repo`, map[string]string{
		"TEMP": `C:\tmp`,
		"TMP":  `C:\tmp`,
	})
	if err != nil {
		t.Fatalf("TokenModeForPermissionProfile() error = %v", err)
	}
	if mode != WindowsSandboxTokenModeWritableRootsCapability {
		t.Fatalf("mode = %s", mode)
	}
	permissions, err := ResolvePermissions(&profile, nil)
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}
	roots := permissions.WritableRootsForCWD(`C:\repo`, map[string]string{"TEMP": `C:\tmp`, "TMP": `C:\tmp`})
	if len(roots) != 2 {
		t.Fatalf("roots = %#v, want cwd and temp", roots)
	}
}

func TestTokenModeForReadOnlyProfileUsesReadOnlyCapability(t *testing.T) {
	profile := coresandbox.ReadOnlyPermissionProfile()
	mode, err := TokenModeForPermissionProfile(&profile, nil, `C:\repo`, nil)
	if err != nil {
		t.Fatalf("TokenModeForPermissionProfile() error = %v", err)
	}
	if mode != WindowsSandboxTokenModeReadOnlyCapability {
		t.Fatalf("mode = %s", mode)
	}
}

func TestResolvePermissionsRejectsDisabledProfile(t *testing.T) {
	profile := coresandbox.FullAccessPermissionProfile()
	if _, err := ResolvePermissions(&profile, nil); err == nil {
		t.Fatalf("ResolvePermissions(full access) error = nil, want failure")
	}
}
