package gitutil

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RepositoryIdentity is the stable on-disk identity shared by a repository and
// its linked worktrees (Rust git-utils worktree.rs, #43279).
type RepositoryIdentity struct {
	// CommonDir is the canonical shared Git administrative directory.
	CommonDir string
	// RelativeCWD is the directory within the current checkout, preserved
	// across linked worktrees.
	RelativeCWD string
	// PrimaryRoot is the canonical root of the repository's primary checkout.
	PrimaryRoot string
}

func canonicalizeNative(path string) (string, bool) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", false
	}
	return filepath.Clean(abs), true
}

// getGitRepoRoot walks up the directory hierarchy looking for a `.git` file or
// directory, mirroring Rust git-utils info.rs::get_git_repo_root. It does not
// require the git binary.
func getGitRepoRoot(baseDir string) (string, bool) {
	base := baseDir
	if info, err := os.Stat(base); err == nil && !info.IsDir() {
		parent := filepath.Dir(base)
		if parent == base {
			return "", false
		}
		base = parent
	}
	dir := base
	for {
		dotGit := filepath.Join(dir, ".git")
		if _, err := os.Lstat(dotGit); err == nil {
			info, statErr := os.Stat(dotGit)
			if statErr == nil {
				if !info.IsDir() {
					return dir, true
				}
				if _, headErr := os.Stat(filepath.Join(dotGit, "HEAD")); headErr == nil {
					return dir, true
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// RepositoryIdentityForCWD identifies a checkout without executing Git or
// trusting unchecked administrative links.
func RepositoryIdentityForCWD(cwd string) (*RepositoryIdentity, bool) {
	canonicalCWD, ok := canonicalizeNative(cwd)
	if !ok {
		return nil, false
	}
	if info, err := os.Stat(canonicalCWD); err != nil || !info.IsDir() {
		return nil, false
	}

	checkoutRootRaw, ok := getGitRepoRoot(canonicalCWD)
	if !ok {
		return nil, false
	}
	checkoutRoot, ok := canonicalizeNative(checkoutRootRaw)
	if !ok {
		return nil, false
	}
	relativeCWD, ok := relativeWithin(checkoutRoot, canonicalCWD)
	if !ok {
		return nil, false
	}

	gitEntry := filepath.Join(checkoutRoot, ".git")
	entryInfo, err := os.Lstat(gitEntry)
	if err != nil {
		return nil, false
	}
	if entryInfo.Mode()&os.ModeSymlink != 0 {
		return nil, false
	}

	var commonDir string
	switch {
	case entryInfo.IsDir():
		commonDir, ok = canonicalizeNative(gitEntry)
		if !ok {
			return nil, false
		}
	case entryInfo.Mode().IsRegular():
		gitDir, ok := readGitPath(gitEntry, checkoutRoot, "gitdir:")
		if !ok {
			return nil, false
		}
		if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
			return nil, false
		}
		commonDir, ok = readGitPath(filepath.Join(gitDir, "commondir"), gitDir, "")
		if !ok {
			return nil, false
		}
		if info, err := os.Stat(commonDir); err != nil || !info.IsDir() {
			return nil, false
		}
		registeredRoot, ok := canonicalizeNative(filepath.Join(commonDir, "worktrees"))
		if !ok || filepath.Dir(gitDir) != registeredRoot {
			return nil, false
		}
		backlink, ok := readGitPath(filepath.Join(gitDir, "gitdir"), gitDir, "")
		canonicalGitEntry, entryOK := canonicalizeNative(gitEntry)
		if !ok || !entryOK || backlink != canonicalGitEntry {
			return nil, false
		}
	default:
		return nil, false
	}

	primaryRoot := filepath.Dir(commonDir)
	primaryGitEntry := filepath.Join(primaryRoot, ".git")
	primaryInfo, err := os.Lstat(primaryGitEntry)
	if err != nil || !primaryInfo.IsDir() || primaryInfo.Mode()&os.ModeSymlink != 0 {
		return nil, false
	}
	canonicalPrimaryGit, ok := canonicalizeNative(primaryGitEntry)
	if !ok || canonicalPrimaryGit != commonDir {
		return nil, false
	}

	return &RepositoryIdentity{
		CommonDir:   commonDir,
		RelativeCWD: relativeCWD,
		PrimaryRoot: primaryRoot,
	}, true
}

// LinkedWorktreeCWDs returns corresponding existing directories in the current,
// primary, and linked checkouts, in discovery order.
func LinkedWorktreeCWDs(cwd string) ([]string, bool) {
	identity, ok := RepositoryIdentityForCWD(cwd)
	if !ok {
		return nil, false
	}
	currentCWD, ok := canonicalizeNative(cwd)
	if !ok {
		return nil, false
	}
	result := []string{cwd}
	seen := map[string]bool{cwd: true}
	if !seen[currentCWD] {
		seen[currentCWD] = true
		result = append(result, currentCWD)
	}

	result = appendLinkedCWD(result, seen, identity.PrimaryRoot, identity)

	worktrees := filepath.Join(identity.CommonDir, "worktrees")
	if _, err := os.Stat(worktrees); err != nil {
		return result, true
	}
	worktrees, ok = canonicalizeNative(worktrees)
	if !ok {
		return nil, false
	}
	entries, err := os.ReadDir(worktrees)
	if err != nil {
		return nil, false
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		gitDir, ok := canonicalizeNative(filepath.Join(worktrees, name))
		if !ok || filepath.Dir(gitDir) != worktrees {
			continue
		}
		gitFile, ok := readGitPath(filepath.Join(gitDir, "gitdir"), gitDir, "")
		if !ok || filepath.Base(gitFile) != ".git" {
			continue
		}
		result = appendLinkedCWD(result, seen, filepath.Dir(gitFile), identity)
	}
	return result, true
}

func appendLinkedCWD(result []string, seen map[string]bool, checkoutRoot string, identity *RepositoryIdentity) []string {
	canonicalRoot, ok := canonicalizeNative(checkoutRoot)
	if !ok {
		return result
	}
	candidate, ok := canonicalizeNative(filepath.Join(canonicalRoot, identity.RelativeCWD))
	if !ok || !isDir(candidate) || !pathStartsWith(candidate, canonicalRoot) {
		return result
	}
	candidateIdentity, ok := RepositoryIdentityForCWD(candidate)
	if !ok {
		return result
	}
	if candidateIdentity.CommonDir == identity.CommonDir &&
		candidateIdentity.RelativeCWD == identity.RelativeCWD &&
		!seen[candidate] {
		seen[candidate] = true
		result = append(result, candidate)
	}
	return result
}

func readGitPath(path string, relativeTo string, prefix string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(string(data))
	value = strings.TrimPrefix(value, prefix)
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\n\r") {
		return "", false
	}
	return canonicalizeNative(filepath.Join(relativeTo, value))
}

// relativeWithin mirrors Rust Path::strip_prefix: it reports the relative path
// only when target lives under root.
func relativeWithin(root string, target string) (string, bool) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return rel, true
}

func pathStartsWith(path string, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
