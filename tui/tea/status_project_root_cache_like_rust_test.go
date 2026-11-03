package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust ChatWidget::status_line_project_root_name caches the resolved project
// root for the active cwd; the status line renders every frame, so the ancestor
// probe must not run on the draw path.
func TestStatusProjectRootCachesPerCWDLikeRust(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, ".git")
	if err := os.Mkdir(marker, 0o755); err != nil {
		t.Fatal(err)
	}
	// Rust #39629: a .git directory only counts as repository metadata when it
	// looks like a real checkout, so give it a HEAD.
	if err := os.WriteFile(filepath.Join(marker, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "nested")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})
	gotRoot, gotName := model.statusProjectRoot(sub)
	if !samePath(gotRoot, root) {
		t.Fatalf("project root = %q, want %q", gotRoot, root)
	}
	if gotName != filepath.Base(root) {
		t.Fatalf("project name = %q, want %q", gotName, filepath.Base(root))
	}

	// Removing the marker must not change the answer while the cache is warm:
	// that proves the second lookup did not touch the filesystem.
	if err := os.RemoveAll(marker); err != nil {
		t.Fatal(err)
	}
	if cached, _ := model.statusProjectRoot(sub); !samePath(cached, root) {
		t.Fatalf("cached root = %q after marker removal, want %q", cached, root)
	}

	model.invalidateStatusProjectRootCache()
	if refreshed, _ := model.statusProjectRoot(sub); !samePath(refreshed, sub) {
		t.Fatalf("root after invalidation = %q, want %q", refreshed, sub)
	}
}

// The cache is keyed by cwd, so a directory change re-resolves on its own.
func TestStatusProjectRootCacheMissesOnCWDChange(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	model := NewModel(codextui.NewState(nil), Options{Width: 80, Height: 24})

	model.statusProjectRoot(first)
	if got, _ := model.statusProjectRoot(second); !samePath(got, second) {
		t.Fatalf("root for second cwd = %q, want %q", got, second)
	}
}

func samePath(a string, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
