package gitutil

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const discoveryTimeout = 2 * time.Second

var discoverySemaphore = make(chan struct{}, 32)

type rootDiscoveryRun struct {
	done chan struct{}
	root string
	err  error
}

var (
	rootDiscoveryMu   sync.Mutex
	rootDiscoveryRuns = map[string]*rootDiscoveryRun{}
)

// DiscoverGitRoot resolves the repository root for cwd while coalescing
// concurrent lookups for the same working directory and bounding the number of
// filesystem probes (Rust #42132). Results are discarded after completion so a
// later call observes repository changes rather than caching a stale root.
func DiscoverGitRoot(ctx context.Context, cwd string) (string, error) {
	cwd = filepath.Clean(cwd)
	key := cwd
	run := shareRootDiscoveryRun(key, func() (string, error) {
		discoverySemaphore <- struct{}{}
		defer func() { <-discoverySemaphore }()
		root, err := RunWithTimeout(context.Background(), discoveryTimeout, cwd, "rev-parse", "--show-toplevel")
		return strings.TrimSpace(root), err
	})
	select {
	case <-run.done:
		return run.root, run.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func shareRootDiscoveryRun(key string, initiator func() (string, error)) *rootDiscoveryRun {
	rootDiscoveryMu.Lock()
	if run, ok := rootDiscoveryRuns[key]; ok {
		rootDiscoveryMu.Unlock()
		return run
	}
	run := &rootDiscoveryRun{done: make(chan struct{})}
	rootDiscoveryRuns[key] = run
	rootDiscoveryMu.Unlock()

	go func() {
		run.root, run.err = initiator()
		close(run.done)
		rootDiscoveryMu.Lock()
		if rootDiscoveryRuns[key] == run {
			delete(rootDiscoveryRuns, key)
		}
		rootDiscoveryMu.Unlock()
	}()
	return run
}
