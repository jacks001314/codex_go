package review

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"codex_go/internal/cli"
	"codex_go/internal/prompt"
)

type Target struct {
	Kind         string
	Base         string
	Commit       string
	CommitTitle  string
	Instructions string
}

type DiffProvider interface {
	Diff(Target) (string, error)
}

func BuildTarget(opts cli.ReviewOptions, stdin io.Reader) (Target, error) {
	switch {
	case opts.Uncommitted:
		return Target{Kind: "uncommitted"}, nil
	case opts.Base != "":
		return Target{Kind: "base", Base: opts.Base}, nil
	case opts.Commit != "":
		return Target{Kind: "commit", Commit: opts.Commit, CommitTitle: opts.CommitTitle}, nil
	case opts.Prompt != "":
		prompt, err := prompt.Resolve(opts.Prompt, stdin)
		if err != nil {
			return Target{}, err
		}
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			return Target{}, errors.New("review prompt cannot be empty")
		}
		return Target{Kind: "custom", Instructions: prompt}, nil
	default:
		return Target{}, errors.New("specify --uncommitted, --base, --commit, or provide custom review instructions")
	}
}

func BuildPromptFromOptions(opts cli.ReviewOptions, stdin io.Reader, provider DiffProvider) (string, Target, error) {
	target, err := BuildTarget(opts, stdin)
	if err != nil {
		return "", Target{}, err
	}
	if target.Kind == "custom" {
		return PromptForTarget(target), target, nil
	}
	if provider == nil {
		provider = &GitDiffProvider{}
	}
	diff, err := provider.Diff(target)
	if err != nil {
		return "", Target{}, err
	}
	return PromptWithDiff(target, diff), target, nil
}

func PromptForTarget(target Target) string {
	return PromptWithDiff(target, "")
}

func PromptWithDiff(target Target, diff string) string {
	header := promptHeader(target)
	if target.Kind == "custom" {
		return header
	}
	diff = strings.TrimRight(diff, "\n")
	if strings.TrimSpace(diff) == "" {
		return header + "\n\nNo git diff was found for this review target."
	}
	return header + "\n\nGit diff:\n```diff\n" + diff + "\n```"
}

func promptHeader(target Target) string {
	switch target.Kind {
	case "uncommitted":
		return "Review uncommitted changes."
	case "base":
		return fmt.Sprintf("Review changes against base branch %s.", target.Base)
	case "commit":
		if target.CommitTitle != "" {
			return fmt.Sprintf("Review commit %s (%s).", target.Commit, target.CommitTitle)
		}
		return fmt.Sprintf("Review commit %s.", target.Commit)
	case "custom":
		return "Review with custom instructions: " + target.Instructions
	default:
		return "Review changes."
	}
}
