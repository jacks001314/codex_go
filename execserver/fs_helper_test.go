package execserver

import (
	"path/filepath"
	"testing"
)

func TestFSHelperReadableRootsContainsOnlyTheHelperExecutableLikeRust(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "codex.exe")
	roots := fsHelperReadableRoots(executable)
	if len(roots) != 1 || roots[0] != filepath.Clean(executable) {
		t.Fatalf("readable roots = %#v, want only the helper executable", roots)
	}
	if parent := filepath.Dir(executable); len(roots) == 1 && roots[0] == parent {
		t.Fatalf("readable root = %q, want the executable, not its parent directory", roots[0])
	}
	if roots := fsHelperReadableRoots(""); roots != nil {
		t.Fatalf("empty executable readable roots = %#v, want nil", roots)
	}
}
