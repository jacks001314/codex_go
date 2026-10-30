//go:build windows

package mxc

import (
	"os"
	"path/filepath"
	"testing"
)

// Mirrors Rust #57985: only writable volume roots are expanded into their
// current children; read-only volume roots stay recursive on their own.
func TestOnlyWriteVolumeRootsExpandLikeRust(t *testing.T) {
	volume := t.TempDir()
	child := filepath.Join(volume, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	write := expandVolumeRootPaths([]string{volume}, []string{volume})
	if _, ok := write[canonicalKeyForTest(child)]; !ok {
		t.Fatalf("write expansion must include the child: %#v", write)
	}
	read := expandReadVolumeRoots([]string{volume})
	if len(read) != 1 {
		t.Fatalf("read roots must not be expanded: %#v", read)
	}
	if _, ok := read[canonicalKeyForTest(child)]; ok {
		t.Fatalf("read expansion must not include the child: %#v", read)
	}
}

func canonicalKeyForTest(path string) string {
	return filepath.Clean(path)
}

// Mirrors Rust #57783: default reads keep local volumes and cwd shares but drop
// unrelated network shares without probing them.
func TestImplicitReadRootsExcludeUnrelatedSharesLikeRust(t *testing.T) {
	original := driveTypeOfRoot
	defer func() { driveTypeOfRoot = original }()
	driveTypeOfRoot = func(root string) uint32 {
		if root == `Z:\` {
			return driveRemote
		}
		return 3 // DRIVE_FIXED
	}
	roots := ImplicitReadRoots([]string{`C:\`, `Z:\`}, `C:\work`, `C:\work\repo`)
	if len(roots) != 1 || roots[0] != `C:\` {
		t.Fatalf("implicit roots = %#v, want only the local volume", roots)
	}
	roots = ImplicitReadRoots([]string{`C:\`, `Z:\`}, `Z:\share\project`, `Z:\share\project\repo`)
	if len(roots) != 2 {
		t.Fatalf("implicit roots for a cwd share = %#v", roots)
	}
}

// Mirrors Rust #58168: a deny path below missing directories gets its parents
// created while the denied path itself stays absent.
func TestDenyParentsAreCreatedLikeRust(t *testing.T) {
	root := t.TempDir()
	denied := filepath.Join(root, "missing", "parent", "secret")
	if err := ensureDenyParentDirectories([]string{denied}); err != nil {
		t.Fatalf("ensure deny parents: %v", err)
	}
	if info, err := os.Stat(filepath.Dir(denied)); err != nil || !info.IsDir() {
		t.Fatalf("deny parent was not created: %v", err)
	}
	if _, err := os.Stat(denied); !os.IsNotExist(err) {
		t.Fatalf("deny path itself must stay absent: %v", err)
	}
	if err := ensureDenyParentDirectories([]string{`C:\`}); err != nil {
		t.Fatalf("volume-root deny path must be left alone: %v", err)
	}
}
