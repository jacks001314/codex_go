//go:build linux

package linuxsandbox

import (
	"path/filepath"
	"testing"
)

// Rust #59009: the bubblewrap mount policy projects its filesystem entries
// through the same resolver Seatbelt uses, so grants, denials and platform
// defaults cannot drift between the two backends.
func TestLinuxFilesystemPolicyResolvesEnforcementLikeRust(t *testing.T) {
	cwd := t.TempDir()
	secret := filepath.Join(cwd, "secret.txt")
	policy := &linuxFilesystemPolicy{
		Kind: "restricted",
		Entries: []linuxFilesystemEntry{
			{Access: "read", Special: linuxSpecialPath{Kind: "minimal"}},
			{Access: "write", Path: cwd},
			{Access: "deny", Path: secret},
		},
	}
	resolved, err := policy.resolveForEnforcement(cwd, true)
	if err != nil {
		t.Fatalf("resolveForEnforcement() error = %v", err)
	}
	if !resolved.IncludePlatformDefaults {
		t.Fatal("the minimal read entry did not request platform defaults")
	}
	foundPlatform := false
	for _, root := range resolved.ReadableRoots {
		if root == "/usr" {
			foundPlatform = true
		}
	}
	if !foundPlatform {
		t.Fatalf("readable roots = %#v, want the platform defaults", resolved.ReadableRoots)
	}
	if len(resolved.WritableRoots) != 1 || resolved.WritableRoots[0].Root != cwd {
		t.Fatalf("writable roots = %#v", resolved.WritableRoots)
	}
	if len(resolved.UnreadableRoots) != 1 || resolved.UnreadableRoots[0] != secret {
		t.Fatalf("unreadable roots = %#v", resolved.UnreadableRoots)
	}
}

// The mount policy keeps using the resolved sets for the arguments it emits.
func TestLinuxMountPolicyUsesResolvedReadableRoots(t *testing.T) {
	cwd := t.TempDir()
	policy := &linuxFilesystemPolicy{
		Kind: "restricted",
		Entries: []linuxFilesystemEntry{
			{Access: "read", Path: cwd},
		},
	}
	resolved, err := policy.resolveForEnforcement(cwd, false)
	if err != nil {
		t.Fatalf("resolveForEnforcement() error = %v", err)
	}
	got := policy.readableRoots(cwd)
	if len(got) != 1 || len(resolved.ReadableRoots) != 1 || got[0] != resolved.ReadableRoots[0] {
		t.Fatalf("readableRoots() = %#v, want %#v", got, resolved.ReadableRoots)
	}
}
