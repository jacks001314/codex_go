package gitutil

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const statusTimeout = 2 * time.Second

// statusRunner executes `git status --porcelain` in cwd and returns the raw
// trimmed stdout. It is an interface so tests can inject slow/failing runs.
type statusRunner func(ctx context.Context, cwd string) (string, error)

type statusRun struct {
	done       chan struct{}
	hasChanges bool
	err        error
}

var (
	statusMu   sync.Mutex
	statusRuns = map[string]*statusRun{}
)

// HasChangesInRepo reports whether the repository containing cwd has
// uncommitted changes, coalescing concurrent scans for the same canonical
// repository root into a single in-flight `git status --porcelain`
// invocation (Rust b6c3b51533, #37151). Scans for different repositories
// stay independent and a fresh scan starts after an in-flight request
// completes. The shared run uses its own time budget so cancelling one
// consumer (or its context deadline) does not abort scans other consumers
// rely on.
func HasChangesInRepo(ctx context.Context, cwd, repoRoot string) (bool, error) {
	return hasChangesInRepoWithRunner(ctx, cwd, repoRoot, defaultStatusRunner)
}

func defaultStatusRunner(ctx context.Context, cwd string) (string, error) {
	return RunWithTimeout(ctx, statusTimeout, cwd, "status", "--porcelain")
}

func hasChangesInRepoWithRunner(ctx context.Context, cwd, repoRoot string, runner statusRunner) (bool, error) {
	key := canonicalRepoRoot(repoRoot)
	run := shareStatusRun(key, func() (bool, error) {
		// The shared scan is deliberately not bound to any caller's context
		// and carries no deadline of its own: the runner applies its own time
		// budget (defaultStatusRunner uses a fixed timeout), so a stalled
		// consumer can never abort scans other consumers rely on, and the
		// scan completes on its own schedule.
		output, err := runner(context.Background(), cwd)
		if err != nil {
			return false, err
		}
		return strings.TrimSpace(output) != "", nil
	})
	return waitStatus(ctx, run)
}

// shareStatusRun returns the in-flight run for key, starting one with the
// initiator function when none exists. Consumers that arrive while a run is
// registered deterministically receive the same run handle (mirroring Rust's
// WeakShared<GitStatusFuture> upgrade), so coalescing is exact rather than
// best-effort under scheduling.
func shareStatusRun(key string, initiator func() (bool, error)) *statusRun {
	statusMu.Lock()
	if run, ok := statusRuns[key]; ok {
		statusMu.Unlock()
		return run
	}
	run := &statusRun{done: make(chan struct{})}
	statusRuns[key] = run
	statusMu.Unlock()

	go func() {
		run.hasChanges, run.err = initiator()
		close(run.done)
		statusMu.Lock()
		if statusRuns[key] == run {
			delete(statusRuns, key)
		}
		statusMu.Unlock()
	}()
	return run
}

func waitStatus(ctx context.Context, run *statusRun) (bool, error) {
	select {
	case <-run.done:
		return run.hasChanges, run.err
	case <-ctx.Done():
		return false, ctx.Err()
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
