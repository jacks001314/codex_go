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

// TestComputeAllowPathsDeniesAWSMetadataLikeRust mirrors Rust #48176's Windows
// sandbox coverage: an existing `.aws` (and `.agents`) directory inside a
// writable root is denied, while a sibling file stays writable.
func TestComputeAllowPathsDeniesAWSMetadataLikeRust(t *testing.T) {
	workspace := t.TempDir()
	metadataDirs := map[string]string{}
	for _, name := range []string{".git", ".agents", ".aws"} {
		dir := filepath.Join(workspace, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		metadataDirs[name] = dir
	}
	profile := coresandbox.WorkspaceWritePermissionProfile()
	permissions, err := ResolvePermissions(&profile, nil)
	if err != nil {
		t.Fatalf("ResolvePermissions() error = %v", err)
	}

	paths := ComputeAllowPathsForPermissions(permissions, workspace, map[string]string{"TEMP": filepath.Join(workspace, "missing-temp")})
	for name, dir := range metadataDirs {
		canonical, _ := CanonicalizePath(dir)
		if _, ok := paths.Deny[canonical]; !ok {
			t.Fatalf("%s missing from deny paths %#v", name, paths.DenySlice())
		}
	}
	workspaceCanonical, _ := CanonicalizePath(workspace)
	if _, ok := paths.Allow[workspaceCanonical]; !ok {
		t.Fatalf("allow paths = %#v, want the workspace root", paths.AllowSlice())
	}
}
