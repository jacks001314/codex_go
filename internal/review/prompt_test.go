package review

import (
	"strings"
	"testing"
)

func TestPromptForCommitWithTitle(t *testing.T) {
	title := "fix parser"
	prompt, err := Prompt(&PromptTarget{Kind: PromptCommit, SHA: "abcdef123", Title: &title})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	want := "Review the code changes introduced by commit abcdef123 (\"fix parser\"). Provide prioritized, actionable findings."
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestPromptForBaseBranchBackupMatchesRustTemplate(t *testing.T) {
	prompt, err := Prompt(&PromptTarget{Kind: PromptBaseBranch, Branch: "main"})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	want := "Review the code changes against the base branch 'main'. Start by finding the merge diff between the current branch and main's upstream e.g. (`git merge-base HEAD \"$(git rev-parse --abbrev-ref \"main@{upstream}\")\"`), then run `git diff` against that SHA to see what changes we would merge into the main branch. Provide prioritized, actionable findings."
	if prompt != want {
		t.Fatalf("prompt = %q, want %q", prompt, want)
	}
}

func TestPromptRejectsEmptyCustom(t *testing.T) {
	if _, err := Prompt(&PromptTarget{Kind: PromptCustom, Instructions: " \n"}); err == nil || err.Error() != "Review prompt cannot be empty" {
		t.Fatalf("error = %v, want Rust empty prompt error", err)
	}
}

func TestResolveDefaultHint(t *testing.T) {
	resolved, err := Resolve(PromptTarget{Kind: PromptBaseBranch, Branch: "main", MergeBaseSHA: "abc"}, "")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.UserFacingHint != "changes against 'main'" || !strings.Contains(resolved.Prompt, "abc") {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestRenderExitSuccessNormalizesLineEndings(t *testing.T) {
	got := RenderExitSuccess("a\r\nb\rc")
	if !strings.Contains(got, "a\nb\nc") {
		t.Fatalf("unexpected exit: %q", got)
	}
}
