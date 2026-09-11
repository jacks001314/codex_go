package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

func TestManagedDenyReadEntriesFromPermsExtractsDenyPaths(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	private := filepath.Join(t.TempDir(), "private")
	perms := map[string]any{
		"filesystem": map[string]any{
			"deny_read": []any{secret, private, filepath.Join(private, "*.key")},
		},
	}
	entries, err := managedDenyReadEntriesFromPerms(perms)
	if err != nil {
		t.Fatalf("managedDenyReadEntriesFromPerms: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 deny_read entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Path.Type != "path" || entries[0].Path.Path != secret || entries[0].Access != sandbox.FileSystemAccessDeny {
		t.Fatalf("entry = %+v", entries[0])
	}
	// A glob keeps its pattern shape instead of being treated as a literal path.
	if entries[2].Path.Type != "glob_pattern" || entries[2].Path.Pattern != filepath.Join(private, "*.key") {
		t.Fatalf("glob entry = %+v", entries[2])
	}

	entries, err = managedDenyReadEntriesFromPerms(map[string]any{
		"filesystem": map[string]any{
			"deny_read": map[string]any{secret: "deny"},
		},
	})
	if err != nil {
		t.Fatalf("managedDenyReadEntriesFromPerms: %v", err)
	}
	if len(entries) != 1 || entries[0].Path.Path != secret {
		t.Fatalf("map-form deny_read entries = %+v", entries)
	}

	// Invalid denials are reported rather than silently skipped.
	if _, err := managedDenyReadEntriesFromPerms(map[string]any{
		"filesystem": map[string]any{"deny_read": []any{"bad\x00path"}},
	}); err == nil {
		t.Fatal("expected invalid deny_read to fail conversion")
	}
}

func TestMergeManagedDenyReadPreservesRulesAndRejectsConflicts(t *testing.T) {
	managed := []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "path", Path: "/secret"},
		Access: sandbox.FileSystemAccessDeny,
	}}
	profile := &sandbox.PermissionProfile{}
	if err := mergeManagedDenyRead(profile, managed); err != nil {
		t.Fatalf("merge should succeed on a permissive profile: %v", err)
	}
	if !profile.HasDenyReadEntries() || !profile.DeniesReadPath(filepath.Clean("/secret")) {
		t.Fatalf("managed deny_read not merged into profile: %+v", profile.DeniedReadEntries)
	}

	// A profile that already denies the path is not a conflict (it is retained).
	profile2 := &sandbox.PermissionProfile{DeniedReadEntries: managed}
	if err := mergeManagedDenyRead(profile2, managed); err != nil {
		t.Fatalf("repeat merge should be allowed: %v", err)
	}

	// A full-access profile conflicts with managed deny_read.
	fullAccess := &sandbox.PermissionProfile{Disabled: true}
	if err := mergeManagedDenyRead(fullAccess, managed); err == nil {
		t.Fatal("full-access profile should be rejected as a conflict")
	}
}
