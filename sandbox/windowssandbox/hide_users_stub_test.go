//go:build !windows

package windowssandbox

import (
	"strings"
	"testing"
)

func TestHideUsersReportsExplicitUnsupportedOffWindows(t *testing.T) {
	if err := HideCurrentUserProfileDir(""); !IsUnsupported(err) || !strings.Contains(err.Error(), "hide_current_user_profile_dir") {
		t.Fatalf("HideCurrentUserProfileDir() error = %v, want explicit unsupported", err)
	}
	if err := HideNewlyCreatedUsers([]string{"CodexSandboxOffline"}, ""); !IsUnsupported(err) || !strings.Contains(err.Error(), "hide_newly_created_users") {
		t.Fatalf("HideNewlyCreatedUsers() error = %v, want explicit unsupported", err)
	}
	if _, err := HideDirectory(t.TempDir()); !IsUnsupported(err) || !strings.Contains(err.Error(), "hide_directory") {
		t.Fatalf("HideDirectory() error = %v, want explicit unsupported", err)
	}
}
