package gitutil

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const statusTimeout = 2 * time.Second

// GitWorktreeStatus is the HEAD and change state observed by one
// `git status --porcelain=v2` scan (Rust codex_git_utils::GitWorktreeStatus,
// #61939).
type GitWorktreeStatus struct {
	// HeadCommitHash is nil for a repository whose HEAD has no commit yet
	// ("(initial)").
	HeadCommitHash *string
	// HasChanges reports ordinary, rename/copy, unmerged, or untracked
	// entries. Uncommitted submodule contents are skipped.
	HasChanges bool
}

var errUnrecognizedWorktreeStatus = errors.New("git status --porcelain=v2 produced an unrecognized status observation")

// statusRunner executes the porcelain-v2 status scan in cwd and returns raw
// stdout. It is a function type so tests can inject slow/failing runs.
type statusRunner func(ctx context.Context, cwd string) (string, error)

type statusRun struct {
	done   chan struct{}
	status *GitWorktreeStatus
	err    error
}

var (
	statusMu   sync.Mutex
	statusRuns = map[string]*statusRun{}
)

// HasChangesInRepo reports whether the repository containing cwd has
// uncommitted changes, coalescing concurrent scans for the same canonical
// repository root into a single in-flight invocation that ignores dirty
// submodule worktrees (Rust b6c3b51533, #37151; #60669). Scans for different
// repositories stay independent and a fresh scan starts after an in-flight
// request completes. The shared run uses its own time budget so cancelling one
// consumer (or its context deadline) does not abort scans other consumers
// rely on.
func HasChangesInRepo(ctx context.Context, cwd, repoRoot string) (bool, error) {
	status, err := GetWorktreeStatusInRepo(ctx, cwd, repoRoot)
	if err != nil {
		return false, err
	}
	return status.HasChanges, nil
}

// GetWorktreeStatusInRepo reads HEAD and worktree changes together from one
// coalesced porcelain-v2 scan, skipping uncommitted submodule contents
// (Rust codex_git_utils::get_worktree_status_in_repo, #61939).
func GetWorktreeStatusInRepo(ctx context.Context, cwd, repoRoot string) (*GitWorktreeStatus, error) {
	return worktreeStatusInRepoWithRunner(ctx, cwd, repoRoot, defaultStatusRunner)
}

func defaultStatusRunner(ctx context.Context, cwd string) (string, error) {
	// Submodule worktree scans can execute clean filters selected by
	// task-writable Git configuration. Skip those scans while still reporting
	// gitlink changes (Rust #60669). --branch adds the HEAD observation used
	// to reject a status that describes a different HEAD than the independent
	// lookup (#61939).
	return RunWithTimeout(ctx, statusTimeout, cwd,
		"status", "--porcelain=v2", "--branch", "--no-ahead-behind", "--ignore-submodules=dirty")
}

func worktreeStatusInRepoWithRunner(ctx context.Context, cwd, repoRoot string, runner statusRunner) (*GitWorktreeStatus, error) {
	key := canonicalRepoRoot(repoRoot)
	run := shareStatusRun(key, func() (*GitWorktreeStatus, error) {
		// The shared scan is deliberately not bound to any caller's context
		// and carries no deadline of its own: the runner applies its own time
		// budget (defaultStatusRunner uses a fixed timeout), so a stalled
		// consumer can never abort scans other consumers rely on, and the
		// scan completes on its own schedule.
		output, err := runner(context.Background(), cwd)
		if err != nil {
			return nil, err
		}
		status, ok := ParseWorktreeStatus(output)
		if !ok {
			return nil, errUnrecognizedWorktreeStatus
		}
		return status, nil
	})
	return waitStatus(ctx, run)
}

// shareStatusRun returns the in-flight run for key, starting one with the
// initiator function when none exists. Consumers that arrive while a run is
// registered deterministically receive the same run handle (mirroring Rust's
// WeakShared upgrade), so coalescing is exact rather than best-effort under
// scheduling.
func shareStatusRun(key string, initiator func() (*GitWorktreeStatus, error)) *statusRun {
	statusMu.Lock()
	if run, ok := statusRuns[key]; ok {
		statusMu.Unlock()
		return run
	}
	run := &statusRun{done: make(chan struct{})}
	statusRuns[key] = run
	statusMu.Unlock()

	go func() {
		run.status, run.err = initiator()
		close(run.done)
		statusMu.Lock()
		if statusRuns[key] == run {
			delete(statusRuns, key)
		}
		statusMu.Unlock()
	}()
	return run
}

func waitStatus(ctx context.Context, run *statusRun) (*GitWorktreeStatus, error) {
	select {
	case <-run.done:
		return run.status, run.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// canonicalRepoRoot keys coalesced scans by the resolved repository root so
// sibling directories and symlink aliases of the same repo share one scan
// (Rust canonicalizes the key the same way).
func canonicalRepoRoot(root string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(root)
}

// ParseWorktreeStatus parses `git status --porcelain=v2 --branch` output into
// the HEAD and change observation it describes (Rust
// codex_git_utils::parse_worktree_status, #61939). Unrecognized output, a
// missing or duplicated `# branch.oid` header, and an invalid object id all
// report false so callers never trust a partial observation.
func ParseWorktreeStatus(output string) (*GitWorktreeStatus, bool) {
	if strings.TrimSpace(output) == "" {
		return nil, false
	}
	status := &GitWorktreeStatus{}
	oid := ""
	oidSet := false
	for _, raw := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if value, ok := strings.CutPrefix(line, "# branch.oid "); ok {
			if oidSet {
				return nil, false
			}
			oidSet = true
			oid = value
			continue
		}
		switch {
		case strings.HasPrefix(line, "# "):
			// Porcelain v2 headers.
		case len(line) >= 2 && strings.IndexByte("12u?", line[0]) >= 0 && line[1] == ' ':
			// Ordinary, rename/copy, unmerged, and untracked entries.
			status.HasChanges = true
		default:
			return nil, false
		}
	}
	if !oidSet {
		return nil, false
	}
	switch {
	case oid == "(initial)":
		status.HeadCommitHash = nil
	case isGitObjectID(oid):
		hash := oid
		status.HeadCommitHash = &hash
	default:
		return nil, false
	}
	return status, true
}

func isGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		isHex := (character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'f') ||
			(character >= 'A' && character <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}
