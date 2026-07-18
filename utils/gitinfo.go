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
	gitDir := filepath.Join(repoRoot, ".git")
	if stat, err := os.Stat(gitDir); err != nil || !stat.IsDir() {
		return nil, false
	}
	info := &GitInfo{}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err == nil {
		parseGitInfoHEAD(strings.TrimSpace(string(head)), info)
	}
	if info.CommitHash == "" && info.Branch != "" {
		if bytes, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash("refs/heads/"+info.Branch))); err == nil {
			info.CommitHash = strings.TrimSpace(string(bytes))
		}
	}
	info.RepositoryURL = readGitInfoOriginURL(filepath.Join(gitDir, "config"))
	return info, true
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
