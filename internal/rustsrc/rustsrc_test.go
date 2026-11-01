package rustsrc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnchorRootsPrefersPinnedWorktree(t *testing.T) {
	root := t.TempDir()
	pinned := filepath.Join(root, "git", "codex-anchor-67f0994b", "codex-rs")
	other := filepath.Join(root, "git", "codex-anchor-ffffffff", "codex-rs")
	for _, dir := range []string{pinned, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[workspace]\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(Cargo.toml) error = %v", err)
		}
	}
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "parity"), 0o755); err != nil {
		t.Fatalf("MkdirAll(parity) error = %v", err)
	}
	manifest := []byte(`{"rustToInclusive":"67f0994b0123456789"}`)
	if err := os.WriteFile(filepath.Join(repo, "parity", "commits.json"), manifest, 0o600); err != nil {
		t.Fatalf("WriteFile(commits.json) error = %v", err)
	}
	pkg := filepath.Join(repo, "somepkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatalf("MkdirAll(somepkg) error = %v", err)
	}
	t.Chdir(pkg)

	roots := AnchorRoots()
	if len(roots) != 2 {
		t.Fatalf("AnchorRoots() = %v, want the two anchor roots", roots)
	}
	if roots[0] != pinned {
		t.Fatalf("AnchorRoots()[0] = %s, want pinned worktree %s", roots[0], pinned)
	}
	if roots[1] != other {
		t.Fatalf("AnchorRoots()[1] = %s, want %s", roots[1], other)
	}

	candidates := Candidates()
	if len(candidates) != len(roots)+5 {
		t.Fatalf("Candidates() = %v, want %d entries", candidates, len(roots)+5)
	}
	last := candidates[len(candidates)-1]
	if want := filepath.Join("..", "codex-main", "codex-rs"); last != want {
		t.Fatalf("Candidates() last = %s, want legacy fallback %s", last, want)
	}
}

func TestAnchorRootsIgnoresWorktreesWithoutCargoToml(t *testing.T) {
	root := t.TempDir()
	broken := filepath.Join(root, "git", "codex-anchor-67f0994b", "codex-rs")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", broken, err)
	}
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("MkdirAll(repo) error = %v", err)
	}
	t.Chdir(repo)

	if roots := AnchorRoots(); len(roots) != 0 {
		t.Fatalf("AnchorRoots() = %v, want no roots without Cargo.toml", roots)
	}
}
