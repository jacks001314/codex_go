// Package rustsrc locates the Rust codex-rs checkout that Go parity tests and
// recording helpers compare against.
//
// Development checkouts keep a plain clone of the Rust repository in a sibling
// git/codex directory, but that clone can lag the revision the parity ledger
// pins. Frozen worktrees (git/codex-anchor-<sha>) are created at the pinned
// commit and never move, so they are preferred whenever they exist.
package rustsrc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	anchorPrefix = "codex-anchor-"
	legacyClone  = "codex"
	manifestRel  = "parity/commits.json"
	// parentWalk bounds how many directories above the working directory are
	// searched for git/ and the parity ledger.
	parentWalk = 6
	// shortCommitLen is the shortest frozen-commit prefix used to name anchor
	// worktrees.
	shortCommitLen = 8
)

// AnchorRoots returns the codex-rs roots of the frozen codex-anchor-* worktrees
// reachable from the current directory, best match first: worktrees named after
// the pinned commit lead, then any remaining anchor worktrees by descending
// name. Roots without a Cargo.toml are dropped.
func AnchorRoots() []string {
	frozen := frozenCommit()
	var roots []string
	seen := make(map[string]bool)
	for _, gitDir := range gitDirs() {
		for _, name := range anchorNames(gitDir, frozen) {
			root := filepath.Join(gitDir, name, "codex-rs")
			if seen[root] {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, "Cargo.toml")); err != nil {
				continue
			}
			seen[root] = true
			roots = append(roots, root)
		}
	}
	return roots
}

// Candidates returns the Rust codex-rs roots to probe in order: the frozen
// anchor worktrees first, then the legacy sibling checkouts. Callers prepend
// CODEX_RUST_ROOT and keep their own validation.
func Candidates() []string {
	candidates := AnchorRoots()
	return append(candidates,
		filepath.Join("..", "..", "git", legacyClone, "codex-rs"),
		filepath.Join("..", "..", "..", "git", legacyClone, "codex-rs"),
		filepath.Join("..", "..", "codex-main", "codex-rs"),
		filepath.Join("..", "..", "..", "codex-main", "codex-rs"),
		filepath.Join("..", "codex-main", "codex-rs"),
	)
}

// gitDirs lists the git directories among the ancestors of the working
// directory, nearest first.
func gitDirs() []string {
	var dirs []string
	for _, dir := range ancestors() {
		candidate := filepath.Join(dir, "git")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			dirs = append(dirs, candidate)
		}
	}
	return dirs
}

// frozenCommit reads rustToInclusive from the parity ledger, searching the
// ancestors of the working directory.
func frozenCommit() string {
	for _, dir := range ancestors() {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(manifestRel)))
		if err != nil {
			continue
		}
		var manifest struct {
			RustToInclusive string `json:"rustToInclusive"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return ""
		}
		return strings.TrimSpace(manifest.RustToInclusive)
	}
	return ""
}

// ancestors returns the working directory followed by its parents, up to
// parentWalk entries.
func ancestors() []string {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	dirs := make([]string, 0, parentWalk)
	for range parentWalk {
		dirs = append(dirs, cwd)
		parent := filepath.Dir(cwd)
		if parent == cwd {
			break
		}
		cwd = parent
	}
	return dirs
}

// anchorNames lists the codex-anchor-* worktree names in gitDir, pinned-commit
// matches first and the rest in descending name order.
func anchorNames(gitDir string, frozen string) []string {
	entries, err := os.ReadDir(gitDir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), anchorPrefix) {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(frozen) < shortCommitLen {
		return names
	}
	prefix := anchorPrefix + frozen[:shortCommitLen]
	pinned := make([]string, 0, 1)
	rest := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			pinned = append(pinned, name)
			continue
		}
		rest = append(rest, name)
	}
	return append(pinned, rest...)
}
