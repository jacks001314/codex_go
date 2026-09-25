package sandbox

import (
	"path/filepath"
	"reflect"
	"testing"
)

// Mirrors Rust's `get_unreadable_roots_with_cwd` / `get_unreadable_globs_with_cwd`:
// a restricted profile reports the deny paths and resolved deny globs it still
// enforces, never the filesystem root, and an unrestricted profile reports none.
func TestUnreadableRestrictionsMatchRust(t *testing.T) {
	cwd := t.TempDir()
	secret := filepath.Join(cwd, "secret")
	globPattern := filepath.Join(cwd, "**", "*.pem")
	workspace := WorkspaceWritePermissionProfile()
	workspace.DeniedReadEntries = []FileSystemSandboxEntry{
		{Path: FileSystemPath{Type: "path", Path: secret}, Access: FileSystemAccessDeny},
		{Path: FileSystemPath{Type: "glob_pattern", Pattern: globPattern}, Access: FileSystemAccessDeny},
		// A duplicated entry must not be listed twice.
		{Path: FileSystemPath{Type: "path", Path: secret}, Access: FileSystemAccessDeny},
	}
	roots := UnreadableRootsWithCWD(&workspace, cwd)
	if !reflect.DeepEqual(roots, []string{cleanRunPath(secret)}) {
		t.Fatalf("unreadable roots = %#v, want %q", roots, cleanRunPath(secret))
	}
	globs := UnreadableGlobsWithCWD(&workspace, cwd)
	if !reflect.DeepEqual(globs, []string{cleanRunPath(globPattern)}) {
		t.Fatalf("unreadable globs = %#v, want %q", globs, cleanRunPath(globPattern))
	}

	// A relative deny glob resolves against the cwd, like Rust's
	// `resolve_path_against_base`.
	relative := WorkspaceWritePermissionPolicyWithDeny(t, cwd, FileSystemSandboxEntry{
		Path:   FileSystemPath{Type: "glob_pattern", Pattern: filepath.Join("keys", "*.pem")},
		Access: FileSystemAccessDeny,
	})
	globs = UnreadableGlobsWithCWD(relative, cwd)
	if !reflect.DeepEqual(globs, []string{cleanRunPath(filepath.Join(cwd, "keys", "*.pem"))}) {
		t.Fatalf("relative deny glob = %#v", globs)
	}

	// The filesystem root is never materialized as a deny root.
	rootDeny := WorkspaceWritePermissionPolicyWithDeny(t, cwd, FileSystemSandboxEntry{
		Path:   FileSystemPath{Type: "path", Path: rootPathForCWD(cwd)},
		Access: FileSystemAccessDeny,
	})
	if roots := UnreadableRootsWithCWD(rootDeny, cwd); len(roots) != 0 {
		t.Fatalf("filesystem root listed as unreadable: %#v", roots)
	}

	// A read-only profile without deny entries and a full-access profile report
	// no restrictions.
	readOnly := ReadOnlyPermissionProfile()
	if roots := UnreadableRootsWithCWD(&readOnly, cwd); len(roots) != 0 {
		t.Fatalf("read-only roots = %#v", roots)
	}
	if globs := UnreadableGlobsWithCWD(&readOnly, cwd); len(globs) != 0 {
		t.Fatalf("read-only globs = %#v", globs)
	}
	full := FullAccessPermissionProfile()
	if roots := UnreadableRootsWithCWD(&full, cwd); len(roots) != 0 {
		t.Fatalf("full-access roots = %#v", roots)
	}
	if globs := UnreadableGlobsWithCWD(&full, cwd); len(globs) != 0 {
		t.Fatalf("full-access globs = %#v", globs)
	}
	if roots := UnreadableRootsWithCWD(nil, cwd); len(roots) != 0 {
		t.Fatalf("nil profile roots = %#v", roots)
	}
}

func WorkspaceWritePermissionPolicyWithDeny(t *testing.T, cwd string, entries ...FileSystemSandboxEntry) *PermissionProfile {
	t.Helper()
	profile := WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = entries
	return &profile
}
