package gitutil

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// GitHeadKind classifies an independent HEAD observation.
type GitHeadKind int

const (
	// GitHeadUnavailable means HEAD could not be observed: not a repository,
	// a spawn failure, or a timeout.
	GitHeadUnavailable GitHeadKind = iota
	// GitHeadCommit means HEAD resolves to a commit.
	GitHeadCommit
	// GitHeadMissing means the repository has no commit yet.
	GitHeadMissing
)

// GitHeadState mirrors Rust codex_git_utils::GitHeadState (#61939).
type GitHeadState struct {
	Kind   GitHeadKind
	Commit string
}

// GetHeadState observes HEAD without failing on an unborn branch:
// `git rev-parse --verify --quiet HEAD` exiting with status 1 means the
// repository has no commit yet, while any other failure leaves HEAD
// unavailable.
func GetHeadState(ctx context.Context, cwd string) GitHeadState {
	output, err := RunWithTimeout(ctx, defaultGitTimeout, cwd, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return GitHeadState{Kind: GitHeadMissing}
		}
		return GitHeadState{Kind: GitHeadUnavailable}
	}
	if commit := strings.TrimSpace(output); commit != "" {
		return GitHeadState{Kind: GitHeadCommit, Commit: commit}
	}
	return GitHeadState{Kind: GitHeadUnavailable}
}
