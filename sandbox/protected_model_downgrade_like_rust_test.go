package sandbox

import "testing"

// TestProtectedModelDowngradeUsesPermissionProfileLikeRust pins Rust #59933:
// the startup restriction for models that require automatic review reads the
// effective permission profile instead of the projected legacy sandbox policy.
func TestProtectedModelDowngradeUsesPermissionProfileLikeRust(t *testing.T) {
	fullAccess := FullAccessPermissionProfile()
	if !fullAccess.RequiresProtectedModelWorkspaceWriteDowngrade() {
		t.Fatal("danger-full-access must still be downgraded")
	}

	fullDiskWrite := func() PermissionProfile {
		profile := WorkspaceWritePermissionProfile()
		profile.NetworkEnabled = true
		profile.SandboxPolicy.fullDiskWriteAccess = true
		profile.SandboxPolicy.fullDiskWriteAccessSet = true
		return profile
	}

	plain := fullDiskWrite()
	if !plain.RequiresProtectedModelWorkspaceWriteDowngrade() {
		t.Fatal("a profile with full disk write and network must be downgraded")
	}

	carveout := fullDiskWrite()
	carveout.DeniedReadEntries = []FileSystemSandboxEntry{{
		Path:   FileSystemPath{Type: "path", Path: `/work/repo/private`},
		Access: FileSystemAccessDeny,
	}}
	if !carveout.RequiresProtectedModelWorkspaceWriteDowngrade() {
		t.Fatal("read-only carveouts must not block the downgrade")
	}

	restricted := fullDiskWrite()
	restricted.NetworkEnabled = false
	if restricted.RequiresProtectedModelWorkspaceWriteDowngrade() {
		t.Fatal("a network-restricted profile must keep its permissions")
	}

	external := PermissionProfile{SandboxPolicy: NewExternalSandboxPolicy(NetworkEnabled)}
	if external.RequiresProtectedModelWorkspaceWriteDowngrade() {
		t.Fatal("external enforcement owns the boundary and must be preserved")
	}

	special := fullDiskWrite()
	special.SandboxPolicy.WritableRoots = []string{":slash_tmp"}
	if special.RequiresProtectedModelWorkspaceWriteDowngrade() {
		t.Fatal("a symbolic writable path cannot be projected locally")
	}

	glob := fullDiskWrite()
	glob.SandboxPolicy.WritableRoots = []string{"/work/**"}
	if glob.RequiresProtectedModelWorkspaceWriteDowngrade() {
		t.Fatal("a glob writable path cannot be projected locally")
	}

	var missing *PermissionProfile
	if missing.RequiresProtectedModelWorkspaceWriteDowngrade() {
		t.Fatal("nil profile must not be downgraded")
	}
}

// TestLegacyWritablePathProjectsOnlyLocalWritePathsLikeRust mirrors Rust
// FileSystemSandboxEntry::legacy_writable_path.
func TestLegacyWritablePathProjectsOnlyLocalWritePathsLikeRust(t *testing.T) {
	path, ok, err := LegacyWritablePath(FileSystemSandboxEntry{
		Path:   FileSystemPath{Type: "path", Path: `/work/repo`},
		Access: FileSystemAccessWrite,
	})
	if err != nil || !ok || path != `/work/repo` {
		t.Fatalf("write path = (%q, %v, %v), want (/work/repo, true, nil)", path, ok, err)
	}

	for name, entry := range map[string]FileSystemSandboxEntry{
		"read entry": {Path: FileSystemPath{Type: "path", Path: `/work/repo`}, Access: FileSystemAccessRead},
		"special":    {Path: FileSystemPath{Type: "special", Value: &FileSystemSpecialPath{Kind: "slash_tmp"}}, Access: FileSystemAccessWrite},
		"glob":       {Path: FileSystemPath{Type: "glob_pattern", Pattern: `/work/**`}, Access: FileSystemAccessWrite},
	} {
		if _, ok, err := LegacyWritablePath(entry); ok || err != nil {
			t.Fatalf("%s: (ok=%v, err=%v), want (false, nil)", name, ok, err)
		}
	}

	if _, ok, err := LegacyWritablePath(FileSystemSandboxEntry{
		Path:   FileSystemPath{Type: "path", Path: `relative/repo`},
		Access: FileSystemAccessWrite,
	}); ok || err == nil {
		t.Fatalf("relative path: (ok=%v, err=%v), want an error", ok, err)
	}
}
