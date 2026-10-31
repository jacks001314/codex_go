package review

import (
	"errors"
	"io"
	"strings"

	"codex_go/cli"
	"codex_go/prompt"
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

type mergeBaseProvider interface {
	MergeBaseWithHead(branch string) (string, error)
}

func BuildTarget(opts cli.ReviewOptions, stdin io.Reader) (Target, error) {
	return BuildTargetWithStderr(opts, stdin, io.Discard)
}

// BuildTargetWithStderr resolves the review target the way Rust's
// exec::build_review_request does. A custom prompt uses resolve_prompt, so a
// positional prompt is taken verbatim, `-` forces stdin and piped stdin is only
// read for the `-` sentinel; Rust's stdin notices go to stderr.
func BuildTargetWithStderr(opts cli.ReviewOptions, stdin io.Reader, stderr io.Writer) (Target, error) {
	switch {
	case opts.Uncommitted:
		return Target{Kind: "uncommitted"}, nil
	case opts.Base != "":
		return Target{Kind: "base", Base: opts.Base}, nil
	case opts.Commit != "":
		return Target{Kind: "commit", Commit: opts.Commit, CommitTitle: opts.CommitTitle}, nil
	case opts.Prompt != "":
		prompt, err := prompt.ResolveExecPrompt(opts.Prompt, stdin, stderr)
		if err != nil {
			return Target{}, err
		}
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			return Target{}, errors.New("Review prompt cannot be empty")
		}
		return Target{Kind: "custom", Instructions: prompt}, nil
	default:
		return Target{}, errors.New("Specify --uncommitted, --base, --commit, or provide custom review instructions")
	}
}

func BuildPromptFromOptions(opts cli.ReviewOptions, stdin io.Reader, provider DiffProvider) (string, Target, error) {
	return BuildPromptFromOptionsWithStderr(opts, stdin, io.Discard, provider)
}

// BuildPromptFromOptionsWithStderr is BuildPromptFromOptions with Rust's stdin
// notices routed to stderr.
func BuildPromptFromOptionsWithStderr(opts cli.ReviewOptions, stdin io.Reader, stderr io.Writer, provider DiffProvider) (string, Target, error) {
	target, err := BuildTargetWithStderr(opts, stdin, stderr)
	if err != nil {
		return "", Target{}, err
	}
	prompt, err := promptForReviewTarget(target, provider)
	if err != nil {
		return "", Target{}, err
	}
	return prompt, target, nil
}

func PromptForTarget(target Target) string {
	return PromptForTargetInDir(target, "")
}

func PromptForTargetInDir(target Target, dir string) string {
	var provider DiffProvider
	if strings.TrimSpace(dir) != "" {
		provider = &GitDiffProvider{Dir: strings.TrimSpace(dir)}
	}
	prompt, err := promptForReviewTarget(target, provider)
	if err != nil {
		return ""
	}
	return prompt
}

func PromptWithDiff(target Target, _ string) string {
	return PromptForTarget(target)
}

func promptForReviewTarget(target Target, provider DiffProvider) (string, error) {
	promptTarget, err := promptTargetForReviewTarget(target, provider)
	if err != nil {
		return "", err
	}
	return Prompt(promptTarget)
}

func promptTargetForReviewTarget(target Target, provider DiffProvider) (*PromptTarget, error) {
	switch target.Kind {
	case "uncommitted":
		return &PromptTarget{Kind: PromptUncommittedChanges}, nil
	case "base":
		mergeBaseSHA := ""
		if provider == nil {
			provider = &GitDiffProvider{}
		}
		if baseProvider, ok := provider.(mergeBaseProvider); ok {
			var err error
			mergeBaseSHA, err = baseProvider.MergeBaseWithHead(target.Base)
			if err != nil {
				return nil, err
			}
		}
		return &PromptTarget{Kind: PromptBaseBranch, Branch: target.Base, MergeBaseSHA: mergeBaseSHA}, nil
	case "commit":
		var title *string
		if target.CommitTitle != "" {
			titleValue := target.CommitTitle
			title = &titleValue
		}
		return &PromptTarget{Kind: PromptCommit, SHA: target.Commit, Title: title}, nil
	case "custom":
		return &PromptTarget{Kind: PromptCustom, Instructions: target.Instructions}, nil
	default:
		return nil, errors.New("review target is required")
	}
}
