package tui

import (
	"reflect"
	"testing"
)

func TestParseGitNumstatMatchesRust(t *testing.T) {
	stats := ParseGitNumstat("10\t2\tfile.go\n-\t-\tbinary.png\n3\t4\tother.go\nbad\t1\tignored-add\n")
	if stats.Additions != 13 || stats.Deletions != 7 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestOrderedGitRemotesPrioritizesOrigin(t *testing.T) {
	got := OrderedGitRemotes("upstream\norigin\nfork\n")
	want := []string{"origin", "upstream", "fork"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("remotes = %#v, want %#v", got, want)
	}
}

func TestDefaultBranchParsersMatchRust(t *testing.T) {
	exists := func(ref string) bool {
		return ref == "refs/remotes/origin/main" || ref == "refs/heads/master"
	}
	branch, ok := DefaultBranchFromSymbolicRefOutput("refs/remotes/origin/main\n", "origin", exists)
	if !ok || branch.MergeRef != "refs/remotes/origin/main" {
		t.Fatalf("symbolic branch = %#v ok=%v", branch, ok)
	}
	if _, ok := DefaultBranchFromSymbolicRefOutput("refs/remotes/upstream/main", "origin", exists); ok {
		t.Fatal("symbolic ref for different remote should fail")
	}
	branch, ok = DefaultBranchFromRemoteShowOutput("  HEAD branch: main\n", "origin", exists)
	if !ok || branch.MergeRef != "refs/remotes/origin/main" {
		t.Fatalf("remote show branch = %#v ok=%v", branch, ok)
	}
	branch, ok = DefaultBranchLocal(exists)
	if !ok || branch.MergeRef != "refs/heads/master" {
		t.Fatalf("local branch = %#v ok=%v", branch, ok)
	}
}

func TestPullRequestParsersRequireOpenPR(t *testing.T) {
	pr, ok := PullRequestFromViewOutput(`{"number":42,"url":"https://github.test/pr/42","state":"OPEN"}`)
	if !ok || pr.Number != 42 || pr.URL != "https://github.test/pr/42" {
		t.Fatalf("view PR = %#v ok=%v", pr, ok)
	}
	if _, ok := PullRequestFromViewOutput(`{"number":42,"url":"x","state":"MERGED"}`); ok {
		t.Fatal("closed view PR should be ignored")
	}
	pr, ok = PullRequestFromAPIOutput(`[
		{"number":1,"html_url":"https://github.test/pr/1","state":"closed"},
		{"number":2,"html_url":"https://github.test/pr/2","state":"open"}
	]`)
	if !ok || pr.Number != 2 || pr.URL != "https://github.test/pr/2" {
		t.Fatalf("api PR = %#v ok=%v", pr, ok)
	}
}

func TestRepoSearchOrderFromOutputParentFirst(t *testing.T) {
	repos, ok := RepoSearchOrderFromOutput(`{
		"nameWithOwner":"fork/repo",
		"parent":{"nameWithOwner":"upstream/repo"}
	}`)
	if !ok || !reflect.DeepEqual(repos, []string{"upstream/repo", "fork/repo"}) {
		t.Fatalf("repos = %#v ok=%v", repos, ok)
	}
	repos, ok = RepoSearchOrderFromOutput(`{
		"nameWithOwner":"upstream/repo",
		"parent":{"nameWithOwner":"upstream/repo"}
	}`)
	if !ok || !reflect.DeepEqual(repos, []string{"upstream/repo"}) {
		t.Fatalf("deduped repos = %#v ok=%v", repos, ok)
	}
	if _, ok := RepoSearchOrderFromOutput(`{"parent":null}`); ok {
		t.Fatal("missing repo names should fail")
	}
}
