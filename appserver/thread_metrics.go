package appserver

import (
	"context"
	"path/filepath"
	"strings"

	"codex_go/gitutil"
	"codex_go/telemetry"
)

// emitThreadStartedMetric mirrors Rust's THREAD_STARTED_METRIC, recorded when a
// session is constructed: one counter per started/resumed/forked thread, tagged
// by whether the cwd is a git repository and whether it is a linked worktree
// ("unknown" when the checkout cannot be inspected, Rust's Some/None split).
func (r *RuntimeRouter) emitThreadStartedMetric(ctx context.Context, thread *Thread) {
	if r == nil || thread == nil || r.services.TurnMetrics == nil {
		return
	}
	cwd := strings.TrimSpace(thread.CWD)
	r.services.TurnMetrics.Counter(telemetry.ThreadStartedMetric, 1, map[string]string{
		"is_git":      boolTagValue(gitRepositoryRoot(ctx, cwd) != ""),
		"is_worktree": worktreeTagValue(threadIsWorktree(ctx, cwd)),
	})
}

// gitRepositoryRoot mirrors Rust's get_git_repo_root for the is_git tag.
func gitRepositoryRoot(ctx context.Context, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	canonical, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	root, err := gitutil.DiscoverGitRoot(ctx, canonical)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(root)
}

// worktreeTagValue maps the tri-state worktree classification onto Rust's tag
// values.
func worktreeTagValue(isWorktree *bool) string {
	switch {
	case isWorktree == nil:
		return "unknown"
	case *isWorktree:
		return "true"
	default:
		return "false"
	}
}
