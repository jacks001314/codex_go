package review

import (
	"errors"
	"strings"
	"testing"

	"codex_go/cli"
)

func TestBuildTargetUncommitted(t *testing.T) {
	target, err := BuildTarget(cli.ReviewOptions{Uncommitted: true}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("BuildTarget returned error: %v", err)
	}
	if target.Kind != "uncommitted" {
		t.Fatalf("Kind = %q", target.Kind)
	}
}

func TestBuildTargetCommit(t *testing.T) {
	target, err := BuildTarget(cli.ReviewOptions{Commit: "abc123", CommitTitle: "fix"}, strings.NewReader(""))
	if err != nil {
		t.Fatalf("BuildTarget returned error: %v", err)
	}
	if target.Kind != "commit" || target.Commit != "abc123" || target.CommitTitle != "fix" {
		t.Fatalf("target = %#v", target)
	}
}

func TestBuildTargetCustomFromStdin(t *testing.T) {
	target, err := BuildTarget(cli.ReviewOptions{Prompt: "-"}, strings.NewReader("check security\n"))
	if err != nil {
		t.Fatalf("BuildTarget returned error: %v", err)
	}
	if target.Kind != "custom" || target.Instructions != "check security" {
		t.Fatalf("target = %#v", target)
	}
}

func TestBuildTargetRequiresTarget(t *testing.T) {
	_, err := BuildTarget(cli.ReviewOptions{}, strings.NewReader(""))
	if err == nil {
		t.Fatal("BuildTarget returned nil error, want failure")
	}
}

func TestBuildPromptFromOptionsUsesRustReviewPromptWithoutEmbeddingDiff(t *testing.T) {
	prompt, target, err := BuildPromptFromOptions(
		cli.ReviewOptions{Base: "main"},
		strings.NewReader(""),
		&errorDiffProvider{err: errors.New("Diff should not be called")},
	)
	if err != nil {
		t.Fatalf("BuildPromptFromOptions returned error: %v", err)
	}
	if target.Kind != "base" {
		t.Fatalf("target = %#v", target)
	}
	if !strings.Contains(prompt, "Review the code changes against the base branch 'main'.") ||
		!strings.Contains(prompt, "git merge-base HEAD") ||
		!strings.Contains(prompt, "Provide prioritized, actionable findings.") {
		t.Fatalf("prompt missing Rust base review instructions: %q", prompt)
	}
	if strings.Contains(prompt, "Git diff:") || strings.Contains(prompt, "+change") {
		t.Fatalf("prompt embedded diff unexpectedly: %q", prompt)
	}
}

func TestBuildPromptFromOptionsBaseUsesMergeBaseLikeRust(t *testing.T) {
	dir := initGitRepo(t)
	initialBranch := strings.TrimSpace(git(t, dir, "branch", "--show-current"))
	baseSHA := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	git(t, dir, "checkout", "-b", "feature/review")
	writeFile(t, dir, "feature.txt", "feature\n")
	git(t, dir, "add", "feature.txt")
	git(t, dir, "commit", "-m", "feature change")

	prompt, target, err := BuildPromptFromOptions(
		cli.ReviewOptions{Base: initialBranch},
		strings.NewReader(""),
		&GitDiffProvider{Dir: dir},
	)
	if err != nil {
		t.Fatalf("BuildPromptFromOptions returned error: %v", err)
	}
	if target.Kind != "base" {
		t.Fatalf("target = %#v", target)
	}
	if !strings.Contains(prompt, "The merge base commit for this comparison is "+baseSHA+".") ||
		!strings.Contains(prompt, "Run `git diff "+baseSHA+"`") {
		t.Fatalf("prompt = %q, want merge-base SHA %s", prompt, baseSHA)
	}
}

func TestPromptForTargetInDirUsesThreadRepository(t *testing.T) {
	dir := initGitRepo(t)
	initialBranch := strings.TrimSpace(git(t, dir, "branch", "--show-current"))
	baseSHA := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	git(t, dir, "checkout", "-b", "feature/review-cwd")
	writeFile(t, dir, "feature.txt", "feature\n")
	git(t, dir, "add", "feature.txt")
	git(t, dir, "commit", "-m", "feature change")

	prompt := PromptForTargetInDir(Target{Kind: "base", Base: initialBranch}, dir)
	if !strings.Contains(prompt, "The merge base commit for this comparison is "+baseSHA+".") {
		t.Fatalf("prompt = %q, want merge-base SHA %s", prompt, baseSHA)
	}
}

func TestBuildPromptFromOptionsSkipsDiffForCustomPrompt(t *testing.T) {
	provider := &errorDiffProvider{err: errors.New("provider should not be called")}
	prompt, target, err := BuildPromptFromOptions(cli.ReviewOptions{Prompt: "check auth"}, strings.NewReader(""), provider)
	if err != nil {
		t.Fatalf("BuildPromptFromOptions returned error: %v", err)
	}
	if target.Kind != "custom" {
		t.Fatalf("target = %#v", target)
	}
	if prompt != "check auth" {
		t.Fatalf("prompt = %q", prompt)
	}
}

type staticDiffProvider string

func (p *staticDiffProvider) Diff(Target) (string, error) {
	if p == nil {
		return "", nil
	}
	return string(*p), nil
}

type errorDiffProvider struct {
	err error
}

func (p *errorDiffProvider) Diff(Target) (string, error) {
	return "", p.err
}
