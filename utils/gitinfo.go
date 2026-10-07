package utils

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type GitInfo struct {
	CommitHash    string `json:"commit_hash,omitempty"`
	Branch        string `json:"branch,omitempty"`
	RepositoryURL string `json:"repository_url,omitempty"`
}

type GitInfoCommit struct {
	Hash    string
	Subject string
}

func CollectGitInfoFromDir(repoRoot string) (*GitInfo, bool) {
	gitDir, commonDir, ok := gitInfoGitDirs(repoRoot)
	if !ok {
		return nil, false
	}
	info := &GitInfo{}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err == nil {
		parseGitInfoHEAD(strings.TrimSpace(string(head)), info)
	}
	if info.CommitHash == "" && info.Branch != "" {
		// A linked worktree keeps HEAD in its own git dir while the shared branch
		// refs live in the common dir, so consult the common dir first.
		refDirs := []string{commonDir}
		if gitDir != commonDir {
			refDirs = append(refDirs, gitDir)
		}
		for _, dir := range refDirs {
			if bytes, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash("refs/heads/"+info.Branch))); err == nil {
				info.CommitHash = strings.TrimSpace(string(bytes))
				break
			}
		}
	}
	info.RepositoryURL = readGitInfoOriginURL(filepath.Join(commonDir, "config"))
	return info, true
}

// GitOriginURLFromDir returns the `origin` remote URL recorded in the
// repository's `.git/config`, mirroring Rust git-utils `get_git_origin_url`
// (#49076). Unlike CollectGitInfoFromDir it reads only the origin URL, so the
// skill invocation analytics path no longer collects the commit hash and branch
// it never uses. The bool reports whether repoRoot is a Git work tree.
//
// The `.git` entry may be a directory or a gitfile: Rust git-utils
// `get_git_repo_root` walks up looking for "a `.git` file or directory" and
// `get_git_origin_url` shells out to git, which resolves gitfiles itself. Linked
// worktrees and submodules keep `gitdir: <path>` in that file, so resolving it
// (and the worktree `commondir`) keeps a linked checkout's origin URL visible.
func GitOriginURLFromDir(repoRoot string) (string, bool) {
	_, commonDir, ok := gitInfoGitDirs(repoRoot)
	if !ok {
		return "", false
	}
	return readGitInfoOriginURL(filepath.Join(commonDir, "config")), true
}

// gitInfoGitDirs resolves a work tree's git metadata directories. gitDir is the
// per-work-tree directory that owns HEAD and worktree-local refs; commonDir is
// the shared directory that owns the config and the shared refs. Both are
// "<root>/.git" for an ordinary repository and for a submodule, while a linked
// worktree names "<main>/.git/worktrees/<name>" and points back at "<main>/.git"
// through its `commondir` file. ok is false when repoRoot has no `.git` entry or
// a gitfile does not resolve to a directory.
func gitInfoGitDirs(repoRoot string) (gitDir string, commonDir string, ok bool) {
	entry := filepath.Join(repoRoot, ".git")
	stat, err := os.Stat(entry)
	if err != nil {
		return "", "", false
	}
	if stat.IsDir() {
		return entry, entry, true
	}
	gitDir, ok = resolveGitInfoGitfile(entry, repoRoot)
	if !ok {
		return "", "", false
	}
	return gitDir, gitInfoCommonDir(gitDir), true
}

// resolveGitInfoGitfile reads a `.git` file's `gitdir: <path>` pointer. The path
// is relative to the directory holding the `.git` file and must name a
// directory.
func resolveGitInfoGitfile(path string, baseDir string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(raw))
	if index := strings.IndexAny(line, "\r\n"); index >= 0 {
		line = strings.TrimSpace(line[:index])
	}
	key, value, found := strings.Cut(line, ":")
	if !found || !strings.EqualFold(strings.TrimSpace(key), "gitdir") {
		return "", false
	}
	pointer := strings.TrimSpace(value)
	if pointer == "" {
		return "", false
	}
	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(baseDir, filepath.FromSlash(pointer))
	}
	pointer = filepath.Clean(pointer)
	if stat, err := os.Stat(pointer); err != nil || !stat.IsDir() {
		return "", false
	}
	return pointer, true
}

// gitInfoCommonDir follows the `commondir` pointer a linked worktree's git dir
// carries back to the main repository's git dir. A git dir without one (an
// ordinary repository or a submodule) is its own common dir.
func gitInfoCommonDir(gitDir string) string {
	raw, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	pointer := strings.TrimSpace(string(raw))
	if pointer == "" {
		return gitDir
	}
	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(gitDir, filepath.FromSlash(pointer))
	}
	return filepath.Clean(pointer)
}

func RecentGitInfoCommitsFromLog(logText string, limit int) []GitInfoCommit {
	if limit <= 0 {
		return nil
	}
	scanner := bufio.NewScanner(strings.NewReader(logText))
	commits := make([]GitInfoCommit, 0, limit)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		hash, subject, ok := strings.Cut(line, " ")
		if !ok {
			hash = line
			subject = ""
		}
		commits = append(commits, GitInfoCommit{Hash: hash, Subject: strings.TrimSpace(subject)})
		if len(commits) == limit {
			break
		}
	}
	return commits
}

func GitInfoHasChanges(statusPorcelain string) bool {
	return strings.TrimSpace(statusPorcelain) != ""
}

func GitInfoDiffToRemote(local string, remote string) string {
	local = strings.TrimSpace(local)
	remote = strings.TrimSpace(remote)
	if local == "" || remote == "" || local == remote {
		return ""
	}
	return remote + ".." + local
}

func (i *GitInfo) JSON() (string, error) {
	bytes, err := json.Marshal(i)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func parseGitInfoHEAD(head string, info *GitInfo) {
	if strings.HasPrefix(head, "ref: refs/heads/") {
		info.Branch = strings.TrimPrefix(head, "ref: refs/heads/")
		return
	}
	if head != "" {
		info.CommitHash = head
	}
}

func readGitInfoOriginURL(configPath string) string {
	file, err := os.Open(configPath)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	inOrigin := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		if !inOrigin || !strings.HasPrefix(line, "url") {
			continue
		}
		_, value, ok := strings.Cut(line, "=")
		if ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
