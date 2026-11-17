package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox/sandboxpath"
)

// TestCompatibilityWritableRootsPreserveOutputOnlyFallbackLikeRust mirrors Rust
// #61784: a permission profile that can only express an output-only write grant
// still projects the workspace-write fallback, keeping the output root and the
// cwd with their protected `.git` / `.codex` carveouts.
func TestCompatibilityWritableRootsPreserveOutputOnlyFallbackLikeRust(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	output := filepath.Join(root, "output")
	if err := os.MkdirAll(filepath.Join(output, ".git"), 0o755); err != nil {
		t.Fatalf("create output git directory: %v", err)
	}
	profile := PermissionProfile{SandboxPolicy: &SandboxPolicy{
		Kind:                SandboxWorkspaceWrite,
		WritableRoots:       []string{output},
		ExcludeTmpdirEnvVar: true,
		ExcludeSlashTmp:     true,
	}}

	roots := profile.CompatibilityWritableRoots(cwd)
	if len(roots) != 2 {
		t.Fatalf("compatibility writable roots = %#v, want output and cwd", roots)
	}
	if got := cleanAbs(roots[0].Root); got != cleanAbs(output) {
		t.Fatalf("first writable root = %q, want %q", got, cleanAbs(output))
	}
	if got := cleanAbs(roots[1].Root); got != cleanAbs(cwd) {
		t.Fatalf("second writable root = %q, want %q", got, cleanAbs(cwd))
	}
	for _, protected := range []string{
		filepath.Join(output, ".git"),
		filepath.Join(output, ".codex"),
		filepath.Join(cwd, ".codex"),
	} {
		if !containsCleanPath(roots, protected) {
			t.Fatalf("read-only subpaths = %#v, want %q", roots, protected)
		}
	}
}

// TestProtectedMetadataNamesMatchRustConstant keeps the protected metadata set
// in step with Rust's `PROTECTED_METADATA_PATH_NAMES`.
func TestProtectedMetadataNamesMatchRustConstant(t *testing.T) {
	names := sandboxpath.DefaultProtectedMetadataNames()
	for _, want := range []string{".git", ".agents", ".codex", ".aws"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("protected metadata names = %#v, want %q", names, want)
		}
	}
}

func containsCleanPath(roots []WritableRoot, target string) bool {
	cleanTarget := cleanAbs(target)
	for _, root := range roots {
		for _, subpath := range root.ReadOnlySubpaths {
			if cleanAbs(subpath) == cleanTarget {
				return true
			}
		}
	}
	return false
}
