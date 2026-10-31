package integrity

import (
	"path/filepath"
	"testing"

	"codex_go/sandbox"
	"codex_go/sandbox/sandboxpath"
)

// Rust #41001 `protected_metadata_names_for_writable_root`: a metadata name is
// protected for a writable root unless the policy has a write entry for that
// exact metadata path. A broader writable root above it does not lift the
// protection, a nested rule below it does not either, and a grant for one
// writable root must not unprotect the same name under another root.
func TestProtectedMetadataNamesStayScopedToTheirWritableRootLikeRust(t *testing.T) {
	cwd := t.TempDir()
	rootA := filepath.Join(cwd, "a")
	rootB := filepath.Join(cwd, "b")
	entries := []sandbox.FileSystemSandboxEntry{
		{Path: sandbox.FileSystemPath{Type: "path", Path: cwd}, Access: sandbox.FileSystemAccessWrite},
		{Path: sandbox.FileSystemPath{Type: "path", Path: rootA}, Access: sandbox.FileSystemAccessWrite},
		{Path: sandbox.FileSystemPath{Type: "path", Path: rootB}, Access: sandbox.FileSystemAccessWrite},
	}
	assertProtectedMetadataNames(t, writableRoots(entries, cwd), map[string]bool{
		cwd:   true,
		rootA: true,
		rootB: true,
	})

	granted := append([]sandbox.FileSystemSandboxEntry{
		{Path: sandbox.FileSystemPath{Type: "path", Path: filepath.Join(rootB, ".git")}, Access: sandbox.FileSystemAccessWrite},
	}, entries...)
	assertProtectedMetadataNames(t, writableRoots(granted, cwd), map[string]bool{
		cwd:   true,
		rootA: true,
		rootB: false,
	})

	// A rule below the metadata path grants that subtree only; the metadata path
	// itself stays protected.
	nested := append([]sandbox.FileSystemSandboxEntry{
		{Path: sandbox.FileSystemPath{Type: "path", Path: filepath.Join(rootB, ".git", "objects")}, Access: sandbox.FileSystemAccessWrite},
	}, entries...)
	assertProtectedMetadataNames(t, writableRoots(nested, cwd), map[string]bool{
		cwd:   true,
		rootA: true,
		rootB: true,
	})
}

func assertProtectedMetadataNames(t *testing.T, roots []sandboxpath.WritableRoot, want map[string]bool) {
	t.Helper()
	if len(roots) == 0 {
		t.Fatalf("writableRoots() = %v, want the writable roots", roots)
	}
	for _, root := range roots {
		wantProtected, ok := want[root.Root]
		if !ok {
			continue
		}
		got := containsString(root.ProtectedMetadataNames, ".git")
		if got != wantProtected {
			t.Fatalf("root %q protected metadata = %v, want .git protected = %v", root.Root, root.ProtectedMetadataNames, wantProtected)
		}
	}
}
