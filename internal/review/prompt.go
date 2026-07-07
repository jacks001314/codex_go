package review

import (
	"errors"
	"fmt"
	"strings"
)

const ReviewPrompt = "You are reviewing code changes. Prioritize bugs, regressions, security risks, and missing tests."

type PromptTargetKind string

const (
	PromptUncommittedChanges PromptTargetKind = "uncommitted_changes"
	PromptBaseBranch         PromptTargetKind = "base_branch"
	PromptCommit             PromptTargetKind = "commit"
	PromptCustom             PromptTargetKind = "custom"
)

type PromptTarget struct {
	Kind         PromptTargetKind
	Branch       string
	MergeBaseSHA string
	SHA          string
	Title        *string
	Instructions string
}

type ResolvedRequest struct {
	Target         PromptTarget
	Prompt         string
	UserFacingHint string
}

func Resolve(target PromptTarget, userFacingHint string) (*ResolvedRequest, error) {
	prompt, err := Prompt(&target)
	if err != nil {
		return nil, err
	}
	if userFacingHint == "" {
		userFacingHint = Hint(&target)
	}
	return &ResolvedRequest{Target: target, Prompt: prompt, UserFacingHint: userFacingHint}, nil
}

func Prompt(target *PromptTarget) (string, error) {
	if target == nil {
		return "", errors.New("review target is required")
	}
	switch target.Kind {
	case PromptUncommittedChanges, "":
		return "Review the current code changes (staged, unstaged, and untracked files) and provide prioritized findings.", nil
	case PromptBaseBranch:
		if target.Branch == "" {
			return "", errors.New("base branch is required")
		}
		if target.MergeBaseSHA != "" {
			return fmt.Sprintf("Review the code changes against the base branch %q. The merge base commit for this comparison is %s. Run `git diff %s` to inspect the changes relative to %s. Provide prioritized, actionable findings.", target.Branch, target.MergeBaseSHA, target.MergeBaseSHA, target.Branch), nil
		}
		return fmt.Sprintf("Review the code changes against the base branch %q. Start by finding the merge diff between the current branch and %s's upstream, then run git diff against that SHA. Provide prioritized, actionable findings.", target.Branch, target.Branch), nil
	case PromptCommit:
		if target.SHA == "" {
			return "", errors.New("commit sha is required")
		}
		if target.Title != nil {
			return fmt.Sprintf("Review the code changes introduced by commit %s (%q). Provide prioritized, actionable findings.", target.SHA, *target.Title), nil
		}
		return fmt.Sprintf("Review the code changes introduced by commit %s. Provide prioritized, actionable findings.", target.SHA), nil
	case PromptCustom:
		prompt := strings.TrimSpace(target.Instructions)
		if prompt == "" {
			return "", errors.New("review prompt cannot be empty")
		}
		return prompt, nil
	default:
		return "", fmt.Errorf("unknown review target %q", target.Kind)
	}
}

func Hint(target *PromptTarget) string {
	if target == nil {
		return ""
	}
	switch target.Kind {
	case PromptBaseBranch:
		return "changes against '" + target.Branch + "'"
	case PromptCommit:
		shortSHA := target.SHA
		if len(shortSHA) > 7 {
			shortSHA = shortSHA[:7]
		}
		if target.Title != nil {
			return "commit " + shortSHA + ": " + *target.Title
		}
		return "commit " + shortSHA
	case PromptCustom:
		return strings.TrimSpace(target.Instructions)
	default:
		return "current changes"
	}
}

func RenderExitSuccess(results string) string {
	return "<review_result>\n" + normalizeLineEndings(results) + "\n</review_result>"
}

func RenderExitInterrupted() string {
	return "<review_interrupted />"
}

func normalizeLineEndings(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}
