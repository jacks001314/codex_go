package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

func TestManagedDenyReadEntriesFromPermsExtractsDenyPaths(t *testing.T) {
	perms := map[string]any{
		"filesystem": map[string]any{
			"deny_read": []any{"/tmp/secret", "/home/user/private"},
		},
	}
	entries := managedDenyReadEntriesFromPerms(perms)
	if len(entries) != 2 {
		t.Fatalf("expected 2 deny_read entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Path.Path != filepath.Clean("/tmp/secret") || entries[0].Access != sandbox.FileSystemAccessDeny {
		t.Fatalf("entry = %+v", entries[0])
	}

	entries = managedDenyReadEntriesFromPerms(map[string]any{
		"filesystem": map[string]any{
			"deny_read": map[string]any{"/data/secrets": "deny"},
		},
	})
	if len(entries) != 1 || entries[0].Path.Path != filepath.Clean("/data/secrets") {
		t.Fatalf("map-form deny_read entries = %+v", entries)
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
