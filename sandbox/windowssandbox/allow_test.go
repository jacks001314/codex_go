package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"

	coresandbox "codex_go/sandbox"
)

func TestComputeAllowPathsForPermissionsIncludesWritableRootsAndExistingReadonlySubpaths(t *testing.T) {
	workspace := t.TempDir()
	gitDir := filepath.Join(workspace, ".git")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	profile := coresandbox.WorkspaceWritePermissionProfile()
	permissions, err := ResolvePermissions(&profile, nil)
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}

	paths := ComputeAllowPathsForPermissions(permissions, workspace, map[string]string{"TEMP": filepath.Join(workspace, "missing-temp")})
	workspaceCanonical, _ := CanonicalizePath(workspace)
	gitCanonical, _ := CanonicalizePath(gitDir)
	if _, ok := paths.Allow[workspaceCanonical]; !ok {
		t.Fatalf("allow paths = %#v, want workspace %q", paths.AllowSlice(), workspaceCanonical)
	}
	if _, ok := paths.Deny[gitCanonical]; !ok {
		t.Fatalf("deny paths = %#v, want .git %q", paths.DenySlice(), gitCanonical)
	}
	if len(paths.Deny) != 1 {
		t.Fatalf("deny paths = %#v, want only existing .git", paths.DenySlice())
	}
}
