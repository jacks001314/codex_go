package review

import (
	"errors"
	"strings"
	"testing"

	"codex_go/internal/cli"
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

func TestBuildPromptFromOptionsAddsDiff(t *testing.T) {
	prompt, target, err := BuildPromptFromOptions(
		cli.ReviewOptions{Base: "main"},
		strings.NewReader(""),
		ptr(staticDiffProvider("diff --git a/app.go b/app.go\n+change\n")),
	)
	if err != nil {
		t.Fatalf("BuildPromptFromOptions returned error: %v", err)
	}
	if target.Kind != "base" {
		t.Fatalf("target = %#v", target)
	}
	if !strings.Contains(prompt, "Review changes against base branch main.") {
		t.Fatalf("prompt missing header: %q", prompt)
	}
	if !strings.Contains(prompt, "```diff") || !strings.Contains(prompt, "+change") {
		t.Fatalf("prompt missing diff: %q", prompt)
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
	if prompt != "Review with custom instructions: check auth" {
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

func ptr[T any](value T) *T {
	return &value
}
