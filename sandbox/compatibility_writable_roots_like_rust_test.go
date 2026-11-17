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
	// Rust's default_read_only_subpaths_for_writable_root only carves out
	// metadata that exists, except for the session cwd root, whose missing
	// `.codex` stays protected so first-time creation needs approval.
	wantCarveouts := map[string][]string{
		cleanAbs(output): {cleanAbs(filepath.Join(output, ".git"))},
		cleanAbs(cwd): {
			cleanAbs(filepath.Join(cwd, ".codex")),
			// This port's own project metadata directory follows `.codex`.
			cleanAbs(filepath.Join(cwd, ".gcode")),
		},
	}
	for _, root := range roots {
		want := wantCarveouts[cleanAbs(root.Root)]
		got := make([]string, 0, len(root.ReadOnlySubpaths))
		for _, subpath := range root.ReadOnlySubpaths {
			got = append(got, cleanAbs(subpath))
		}
		if len(got) != len(want) {
			t.Fatalf("read-only subpaths for %q = %#v, want %#v", root.Root, got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("read-only subpaths for %q = %#v, want %#v", root.Root, got, want)
			}
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
