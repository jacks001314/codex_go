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
