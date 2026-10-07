package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCollectFromGitDirBranch(t *testing.T) {
	repo := t.TempDir()
	gitDir := filepath.Join(repo, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(HEAD) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "refs", "heads", "main"), []byte("abc123\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(ref) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[remote \"origin\"]\n  url = git@example.com:repo.git\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	info, ok := CollectGitInfoFromDir(repo)
	if !ok {
		t.Fatalf("CollectGitInfoFromDir() ok = false")
	}
	if info.Branch != "main" || info.CommitHash != "abc123" || info.RepositoryURL != "git@example.com:repo.git" {
		t.Fatalf("CollectGitInfoFromDir() = %#v", info)
	}
}

func TestCollectFromGitDirDetached(t *testing.T) {
	repo := t.TempDir()
	gitDir := filepath.Join(repo, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("deadbeef\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(HEAD) error = %v", err)
	}
	info, ok := CollectGitInfoFromDir(repo)
	if !ok {
		t.Fatalf("CollectGitInfoFromDir() ok = false")
	}
	if info.Branch != "" || info.CommitHash != "deadbeef" {
		t.Fatalf("CollectGitInfoFromDir() = %#v", info)
	}
}

func TestCollectFromGitDirNonGit(t *testing.T) {
	if info, ok := CollectGitInfoFromDir(t.TempDir()); ok || info != nil {
		t.Fatalf("CollectGitInfoFromDir(non git) = %#v/%v, want nil/false", info, ok)
	}
}

func TestRecentCommitsFromLog(t *testing.T) {
	got := RecentGitInfoCommitsFromLog("aaa first\nbbb second\nccc third\n", 2)
	want := []GitInfoCommit{{Hash: "aaa", Subject: "first"}, {Hash: "bbb", Subject: "second"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RecentGitInfoCommitsFromLog() = %#v, want %#v", got, want)
	}
}

func TestHelpers(t *testing.T) {
	if !GitInfoHasChanges(" M file.go\n") || GitInfoHasChanges(" \n") {
		t.Fatalf("GitInfoHasChanges() unexpected result")
	}
	if got := GitInfoDiffToRemote("local", "remote"); got != "remote..local" {
		t.Fatalf("GitInfoDiffToRemote() = %q", got)
	}
	if got := GitInfoDiffToRemote("same", "same"); got != "" {
		t.Fatalf("GitInfoDiffToRemote(same) = %q, want empty", got)
	}
}

func TestInfoJSONOmitsEmptyFields(t *testing.T) {
	text, err := (&GitInfo{Branch: "main"}).JSON()
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := parsed["commit_hash"]; ok {
		t.Fatalf("commit_hash present in %v", parsed)
	}
	if parsed["branch"] != "main" {
		t.Fatalf("branch = %q", parsed["branch"])
	}
}

// Mirrors Rust #49076: the origin-URL reader used by skill analytics returns the
// `origin` remote URL without collecting the commit hash or branch, and reports
// whether the directory is a Git work tree.
func TestGitOriginURLFromDirLikeRust(t *testing.T) {
	repo := t.TempDir()
	gitDir := filepath.Join(repo, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// No HEAD / refs exist: only the origin URL is required.
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[remote \"origin\"]\n  url = git@example.com:repo.git\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	if url, ok := GitOriginURLFromDir(repo); !ok || url != "git@example.com:repo.git" {
		t.Fatalf("GitOriginURLFromDir() = %q/%v, want git@example.com:repo.git/true", url, ok)
	}
	// A repository without an origin remote still reports the work tree.
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n  bare = false\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	if url, ok := GitOriginURLFromDir(repo); !ok || url != "" {
		t.Fatalf("GitOriginURLFromDir(no origin) = %q/%v, want \"\"/true", url, ok)
	}
	if url, ok := GitOriginURLFromDir(t.TempDir()); ok || url != "" {
		t.Fatalf("GitOriginURLFromDir(non git) = %q/%v, want \"\"/false", url, ok)
	}
}

// Mirrors Rust git-utils `get_git_repo_root`, which walks up looking for a
// `.git` file or directory: a linked worktree keeps `gitdir: <path>` in a
// `.git` file, and the pointed-at git dir carries a `commondir` back to the
// main repository. Resolving both keeps the origin URL (and the shared branch
// ref) visible for a linked checkout instead of reporting no work tree.
func TestGitOriginURLFromDirLinkedWorktreeLikeRust(t *testing.T) {
	main := t.TempDir()
	mainGitDir := filepath.Join(main, ".git")
	worktreeGitDir := filepath.Join(mainGitDir, "worktrees", "wt")
	if err := os.MkdirAll(filepath.Join(mainGitDir, "refs", "heads"), 0o755); err != nil {
		t.Fatalf("MkdirAll(main git) error = %v", err)
	}
	if err := os.MkdirAll(worktreeGitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(worktree git) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(mainGitDir, "config"), []byte("[remote \"origin\"]\n  url = git@example.com:linked.git\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(main config) error = %v", err)
	}
	// The linked worktree's git dir owns HEAD and points back at the main git dir.
	if err := os.WriteFile(filepath.Join(worktreeGitDir, "HEAD"), []byte("ref: refs/heads/feature\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(worktree HEAD) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeGitDir, "commondir"), []byte("../..\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(commondir) error = %v", err)
	}
	// The branch ref lives in the common (main) git dir, not the worktree git dir.
	if err := os.WriteFile(filepath.Join(mainGitDir, "refs", "heads", "feature"), []byte("cafe1234\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(feature ref) error = %v", err)
	}

	worktree := filepath.Join(main, "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("MkdirAll(worktree) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+worktreeGitDir+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(worktree .git) error = %v", err)
	}

	if url, ok := GitOriginURLFromDir(worktree); !ok || url != "git@example.com:linked.git" {
		t.Fatalf("GitOriginURLFromDir(linked worktree) = %q/%v, want git@example.com:linked.git/true", url, ok)
	}
	info, ok := CollectGitInfoFromDir(worktree)
	if !ok {
		t.Fatalf("CollectGitInfoFromDir(linked worktree) ok = false")
	}
	if info.Branch != "feature" || info.CommitHash != "cafe1234" || info.RepositoryURL != "git@example.com:linked.git" {
		t.Fatalf("CollectGitInfoFromDir(linked worktree) = %#v", info)
	}
}

// A submodule work tree stores its git metadata under the superproject's
// `.git/modules/<name>` directory. Its `.git` file points straight there and
// the module git dir has no `commondir`, so it is its own common dir.
func TestGitOriginURLFromDirSubmoduleLikeRust(t *testing.T) {
	super := t.TempDir()
	moduleGitDir := filepath.Join(super, ".git", "modules", "sub")
	if err := os.MkdirAll(moduleGitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(module git) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleGitDir, "config"), []byte("[remote \"origin\"]\n  url = git@example.com:sub.git\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(module config) error = %v", err)
	}

	sub := filepath.Join(super, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll(sub) error = %v", err)
	}
	// git writes the pointer relative to the directory holding the `.git` file.
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../.git/modules/sub\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(sub .git) error = %v", err)
	}

	if url, ok := GitOriginURLFromDir(sub); !ok || url != "git@example.com:sub.git" {
		t.Fatalf("GitOriginURLFromDir(submodule) = %q/%v, want git@example.com:sub.git/true", url, ok)
	}
}

// A `.git` file that does not carry a resolvable `gitdir:` pointer is not a work
// tree, so both readers report the same negative result as before.
func TestGitInfoUnresolvableGitfileLikeRust(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("not a gitdir pointer\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.git) error = %v", err)
	}
	if url, ok := GitOriginURLFromDir(repo); ok || url != "" {
		t.Fatalf("GitOriginURLFromDir(bad gitfile) = %q/%v, want \"\"/false", url, ok)
	}
	if info, ok := CollectGitInfoFromDir(repo); ok || info != nil {
		t.Fatalf("CollectGitInfoFromDir(bad gitfile) = %#v/%v, want nil/false", info, ok)
	}
	// A pointer to a directory that does not exist is equally unresolvable.
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: missing/module\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.git) error = %v", err)
	}
	if url, ok := GitOriginURLFromDir(repo); ok || url != "" {
		t.Fatalf("GitOriginURLFromDir(missing gitdir) = %q/%v, want \"\"/false", url, ok)
	}
}
