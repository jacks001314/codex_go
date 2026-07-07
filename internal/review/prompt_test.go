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
	if !strings.Contains(prompt, "abcdef123") || !strings.Contains(prompt, "fix parser") {
		t.Fatalf("unexpected prompt: %s", prompt)
	}
}

func TestPromptRejectsEmptyCustom(t *testing.T) {
	if _, err := Prompt(&PromptTarget{Kind: PromptCustom, Instructions: " \n"}); err == nil {
		t.Fatalf("expected custom prompt error")
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
