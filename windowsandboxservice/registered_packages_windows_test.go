//go:build windows

package windowsandboxservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// packageQueryUserSID returns the process token's user SID string.
func packageQueryUserSID(t *testing.T) string {
	t.Helper()
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Skipf("current process token has no user: %v", err)
	}
	return user.User.Sid.String()
}

func TestRegisteredRuntimePackagesReportsAbsenceLikeRust(t *testing.T) {
	names, err := RegisteredRuntimePackages(packageQueryUserSID(t), "OpenAI.Nonexistent_0000000000000")
	if err != nil {
		t.Fatalf("RegisteredRuntimePackages() error = %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("RegisteredRuntimePackages(nonexistent family) = %v", names)
	}
}

func TestRegisteredRuntimePackagesEnumeratesFullNamesLikeRust(t *testing.T) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		t.Skip("LOCALAPPDATA is not set")
	}
	entries, err := os.ReadDir(filepath.Join(localAppData, "Packages"))
	if err != nil || len(entries) == 0 {
		t.Skipf("no per-user package state to enumerate: %v", err)
	}
	family := entries[0].Name()
	names, err := RegisteredRuntimePackages(packageQueryUserSID(t), family)
	if err != nil {
		t.Fatalf("RegisteredRuntimePackages(%s) error = %v", family, err)
	}
	if len(names) == 0 {
		t.Skipf("family %s is staged without a registration", family)
	}
	for _, name := range names {
		parts := strings.Split(name, "_")
		if len(parts) < 5 || parts[0]+"_"+parts[len(parts)-1] != family {
			t.Fatalf("RegisteredRuntimePackages(%s) returned %q", family, name)
		}
	}
}
