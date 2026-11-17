package gitutil

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxProbeDirectories bounds how many recently modified root directories a
// positive-only change probe inspects (Rust #61939).
const maxProbeDirectories = 4

// WorktreeProbe is a positive-only worktree change observation: it proves the
// repository has changes, but a clean, failed, or incomplete probe never
// proves the complete worktree is clean.
type WorktreeProbe struct {
	RepoRoot string
	Status   GitWorktreeStatus
}

type probeRun struct {
	done  chan struct{}
	probe *WorktreeProbe
}

var (
	probeMu   sync.Mutex
	probeRuns = map[string]*probeRun{}
)

// ProbeWorktreeChanges runs a positive-only change probe over up to four
// recently modified root directories (Rust
// codex_git_utils::probe_worktree_changes, #61939). It returns nil unless the
// probe completed and observed changes. Concurrent probes for the same cwd
// share one in-flight run.
func ProbeWorktreeChanges(ctx context.Context, cwd string) *WorktreeProbe {
	key := filepath.Clean(cwd)
	probeMu.Lock()
	if run, ok := probeRuns[key]; ok {
		probeMu.Unlock()
		return waitProbe(ctx, run)
	}
	run := &probeRun{done: make(chan struct{})}
	probeRuns[key] = run
	probeMu.Unlock()

	go func() {
		run.probe = runChangeProbe(context.Background(), key)
		close(run.done)
		probeMu.Lock()
		if probeRuns[key] == run {
			delete(probeRuns, key)
		}
		probeMu.Unlock()
	}()
	return waitProbe(ctx, run)
}

func waitProbe(ctx context.Context, run *probeRun) *WorktreeProbe {
	select {
	case <-run.done:
		return run.probe
	case <-ctx.Done():
		return nil
	}
}

func runChangeProbe(ctx context.Context, cwd string) *WorktreeProbe {
	root, err := DiscoverGitRoot(ctx, cwd)
	if err != nil || strings.TrimSpace(root) == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	type candidate struct {
		name     string
		modified time.Time
	}
	recent := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == ".git" || !entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		recent = append(recent, candidate{name: entry.Name(), modified: info.ModTime()})
	}
	if len(recent) == 0 {
		return nil
	}
	sort.SliceStable(recent, func(first, second int) bool {
		return recent[first].modified.After(recent[second].modified)
	})
	if len(recent) > maxProbeDirectories {
		recent = recent[:maxProbeDirectories]
	}
	args := []string{"status", "--porcelain=v2", "--branch", "--no-ahead-behind", "--ignore-submodules=dirty", "--"}
	for _, entry := range recent {
		args = append(args, ":(top,literal)"+entry.name)
	}
	output, err := RunWithTimeout(ctx, statusTimeout, cwd, args...)
	if err != nil {
		return nil
	}
	status, ok := ParseWorktreeStatus(output)
	if !ok || !status.HasChanges {
		return nil
	}
	return &WorktreeProbe{RepoRoot: root, Status: *status}
}
