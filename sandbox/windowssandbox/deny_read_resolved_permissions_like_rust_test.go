package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"

	coresandbox "codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"
)

// TestDenyReadExpansionUsesResolvedPermissionsLikeRust pins Rust #59233: the
// native Windows ACL pass expands the resolved enforcement projection (concrete
// denials plus deny globs) instead of the raw policy entries.
func TestDenyReadExpansionUsesResolvedPermissionsLikeRust(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "helper.log")
	allowed := filepath.Join(tmp, "allowed.txt")
	for _, path := range []string{logPath, allowed} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	profile := coresandbox.WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = []coresandbox.FileSystemSandboxEntry{{
		Access: coresandbox.FileSystemAccessDeny,
		Path:   coresandbox.FileSystemPath{Type: "glob_pattern", Pattern: filepath.Join(tmp, "*.log")},
	}}
	resolved, err := coresandbox.ResolveFileSystemPermissionsForEnforcement(&profile, tmp, sandboxpath.ProjectionNative)
	if err != nil {
		t.Fatalf("ResolveFileSystemPermissionsForEnforcement() error = %v", err)
	}
	paths, err := ResolveDenyReadPathsFromResolvedPermissions(resolved, tmp)
	if err != nil {
		t.Fatalf("ResolveDenyReadPathsFromResolvedPermissions() error = %v", err)
	}
	if len(paths) != 1 || !sameWindowsPath(paths[0], logPath) {
		t.Fatalf("deny paths = %#v, want the helper log only", paths)
	}
	fromProfile, err := ResolveDenyReadPathsFromProfile(&profile, tmp)
	if err != nil {
		t.Fatalf("ResolveDenyReadPathsFromProfile() error = %v", err)
	}
	if len(fromProfile) != 1 || !sameWindowsPath(fromProfile[0], logPath) {
		t.Fatalf("profile deny paths = %#v, want the resolved expansion", fromProfile)
	}
	if _, err := os.Stat(allowed); err != nil {
		t.Fatalf("neighboring allowed file unavailable: %v", err)
	}
}

func sameWindowsPath(left string, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}
