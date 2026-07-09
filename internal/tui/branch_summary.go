package tui

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Rust parity: codex-rs/tui/src/branch_summary.rs.

type BranchSummary struct {
	Branch    string
	Additions int
	Deletions int
}

func (s BranchSummary) HasChanges() bool {
	return s.Additions != 0 || s.Deletions != 0
}

type GitBranchDiffStats struct {
	Additions uint64
	Deletions uint64
}

type StatusLineGitSummary struct {
	PullRequest       *StatusLinePullRequest
	BranchChangeStats *GitBranchDiffStats
}

type StatusLinePullRequest struct {
	Number uint64
	URL    string
}

type DefaultBranch struct {
	MergeRef string
}

func ParseGitNumstat(stdout string) GitBranchDiffStats {
	var stats GitBranchDiffStats
	for _, line := range strings.Split(stdout, "\n") {
		columns := strings.Split(line, "\t")
		if len(columns) < 2 {
			continue
		}
		stats.Additions += parseGitNumstatColumn(columns[0])
		stats.Deletions += parseGitNumstatColumn(columns[1])
	}
	return stats
}

func OrderedGitRemotes(stdout string) []string {
	remotes := []string{}
	for _, line := range strings.Split(stdout, "\n") {
		remote := strings.TrimSpace(line)
		if remote != "" {
			remotes = append(remotes, remote)
		}
	}
	for i, remote := range remotes {
		if remote == "origin" {
			copy(remotes[1:i+1], remotes[0:i])
			remotes[0] = "origin"
			break
		}
	}
	return remotes
}

func DefaultBranchFromSymbolicRefOutput(stdout string, remote string, refExists func(string) bool) (DefaultBranch, bool) {
	remote = strings.TrimSpace(remote)
	trimmed := strings.TrimSpace(stdout)
	prefix := "refs/remotes/" + remote + "/"
	if remote == "" || !strings.HasPrefix(trimmed, prefix) {
		return DefaultBranch{}, false
	}
	if refExists != nil && !refExists(trimmed) {
		return DefaultBranch{}, false
	}
	return DefaultBranch{MergeRef: trimmed}, true
}

func DefaultBranchFromRemoteShowOutput(stdout string, remote string, refExists func(string) bool) (DefaultBranch, bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return DefaultBranch{}, false
	}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "HEAD branch:")
		if !ok {
			continue
		}
		name := strings.TrimSpace(rest)
		if name == "" {
			continue
		}
		remoteRef := "refs/remotes/" + remote + "/" + name
		if refExists != nil && !refExists(remoteRef) {
			return DefaultBranch{}, false
		}
		return DefaultBranch{MergeRef: remoteRef}, true
	}
	return DefaultBranch{}, false
}

func DefaultBranchLocal(refExists func(string) bool) (DefaultBranch, bool) {
	for _, candidate := range []string{"main", "master"} {
		localRef := "refs/heads/" + candidate
		if refExists == nil || refExists(localRef) {
			return DefaultBranch{MergeRef: localRef}, true
		}
	}
	return DefaultBranch{}, false
}

func PullRequestFromViewOutput(stdout string) (*StatusLinePullRequest, bool) {
	var payload struct {
		Number uint64 `json:"number"`
		URL    string `json:"url"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		return nil, false
	}
	if !strings.EqualFold(payload.State, "open") {
		return nil, false
	}
	return &StatusLinePullRequest{Number: payload.Number, URL: payload.URL}, true
}

func PullRequestFromAPIOutput(stdout string) (*StatusLinePullRequest, bool) {
	var payload []struct {
		Number uint64 `json:"number"`
		URL    string `json:"html_url"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		return nil, false
	}
	for _, item := range payload {
		if strings.EqualFold(item.State, "open") {
			return &StatusLinePullRequest{Number: item.Number, URL: item.URL}, true
		}
	}
	return nil, false
}

func RepoSearchOrderFromOutput(stdout string) ([]string, bool) {
	var payload struct {
		NameWithOwner *string `json:"nameWithOwner"`
		Parent        *struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"parent"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		return nil, false
	}
	repos := []string{}
	if payload.Parent != nil && strings.TrimSpace(payload.Parent.NameWithOwner) != "" {
		repos = append(repos, strings.TrimSpace(payload.Parent.NameWithOwner))
	}
	if payload.NameWithOwner != nil {
		name := strings.TrimSpace(*payload.NameWithOwner)
		if name != "" && !stringSliceContains(repos, name) {
			repos = append(repos, name)
		}
	}
	if len(repos) == 0 {
		return nil, false
	}
	return repos, true
}

func parseGitNumstatColumn(value string) uint64 {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
