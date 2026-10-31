//go:build windows

package windowsandboxservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestKnownFolderPathResolvesOwnerRootsLikeRust resolves both owner AppData
// roots for the calling token with the flags the metadata grants use.
func TestKnownFolderPathResolvesOwnerRootsLikeRust(t *testing.T) {
	token := metadataTestToken(t)
	local, err := knownFolderPath(token, &knownFolderLocalAppData, ownerFolderFlags)
	if err != nil {
		t.Fatalf("knownFolderPath(LocalAppData) error = %v", err)
	}
	roaming, err := knownFolderPath(token, &knownFolderRoamingAppData, ownerFolderFlags)
	if err != nil {
		t.Fatalf("knownFolderPath(RoamingAppData) error = %v", err)
	}
	if local == "" || roaming == "" || local == roaming {
		t.Fatalf("knownFolderPath() = %q, %q", local, roaming)
	}
	if !filepath.IsAbs(local) || !filepath.IsAbs(roaming) {
		t.Fatalf("knownFolderPath() returned relative paths: %q, %q", local, roaming)
	}
	if expected := os.Getenv("LOCALAPPDATA"); expected != "" && !equalFoldPath(local, expected) {
		t.Fatalf("knownFolderPath(LocalAppData) = %q, want %q", local, expected)
	}
	if _, err := os.Stat(local); err != nil {
		t.Fatalf("stat %s: %v", local, err)
	}
}

func TestKnownFolderPathRejectsMissingIdentifierLikeRust(t *testing.T) {
	token := windows.GetCurrentProcessToken()
	if _, err := knownFolderPath(token, nil, ownerFolderFlags); err == nil {
		t.Fatal("knownFolderPath(nil) = nil")
	}
}

// equalFoldPath compares Windows paths case-insensitively.
func equalFoldPath(left string, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
