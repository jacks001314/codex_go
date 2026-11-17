package appserver

import (
	"context"
	"sync"
	"time"

	"codex_go/codexapi"
	"codex_go/gitutil"
)

// memoryGitMetadataTimeout mirrors Rust's MEMORY_GIT_METADATA_TIMEOUT (#61939):
// memory requests retain whatever Git metadata was observed by the deadline.
const memoryGitMetadataTimeout = time.Second

// memoryGitCollector gathers HEAD, verified remote URLs, and worktree status
// concurrently and publishes each observation as soon as it is known, so a
// slow worktree scan cannot discard an already observed HEAD or remote URL
// (Rust #61939).
type memoryGitCollector struct {
	mu sync.Mutex

	sources memoryGitSources

	remotes map[string]string

	headDone bool
	head     gitutil.GitHeadState

	statusDone bool
	status     *gitutil.GitWorktreeStatus
	statusUsed bool

	latestGitCommitHash string
	hasChanges          *bool
}

// memoryGitSources lets tests drive the three observations independently; the
// production constructor uses the gitutil implementations.
type memoryGitSources struct {
	head           func(ctx context.Context, cwd string) gitutil.GitHeadState
	status         func(ctx context.Context, cwd string, repoRoot string) (*gitutil.GitWorktreeStatus, error)
	observeRemotes func(ctx context.Context, cwd string, publish func(map[string]string))
}

func newMemoryGitCollector() *memoryGitCollector {
	return newMemoryGitCollectorWithSources(memoryGitSources{
		head:   gitutil.GetHeadState,
		status: gitutil.GetWorktreeStatusInRepo,
		observeRemotes: func(ctx context.Context, cwd string, publish func(map[string]string)) {
			gitutil.ObserveGitRemoteURLs(ctx, cwd, publish)
		},
	})
}

func newMemoryGitCollectorWithSources(sources memoryGitSources) *memoryGitCollector {
	return &memoryGitCollector{sources: sources}
}

// collect observes Git metadata until ctx is cancelled or every lookup
// finished.
func (c *memoryGitCollector) collect(ctx context.Context, cwd string, repoRoot string) {
	head := make(chan gitutil.GitHeadState, 1)
	status := make(chan *gitutil.GitWorktreeStatus, 1)
	remotesDone := make(chan struct{})
	go func() { head <- c.sources.head(ctx, cwd) }()
	go func() {
		observed, err := c.sources.status(ctx, cwd, repoRoot)
		if err != nil {
			observed = nil
		}
		status <- observed
	}()
	go func() {
		defer close(remotesDone)
		c.sources.observeRemotes(ctx, cwd, c.observeRemoteURLs)
	}()
	var remotes <-chan struct{} = remotesDone
	for {
		select {
		case state, ok := <-head:
			if ok {
				c.observeHead(state)
			}
			head = nil
		case observed, ok := <-status:
			if ok {
				c.observeStatus(observed)
			}
			status = nil
		case <-remotes:
			remotes = nil
		case <-ctx.Done():
			return
		}
		if head == nil && status == nil && remotes == nil {
			return
		}
	}
}

func (c *memoryGitCollector) observeHead(state gitutil.GitHeadState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.head = state
	c.headDone = true
	if state.Kind == gitutil.GitHeadCommit {
		c.latestGitCommitHash = state.Commit
	}
	c.applyStatusLocked()
}

func (c *memoryGitCollector) observeStatus(observed *gitutil.GitWorktreeStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = observed
	c.statusDone = true
	c.applyStatusLocked()
}

// applyStatusLocked accepts the status observation only once HEAD finished and
// only when it describes the same HEAD; a shared scan can be stale and must
// not resurrect a different commit (Rust #61939).
func (c *memoryGitCollector) applyStatusLocked() {
	if c.statusUsed || !c.headDone || !c.statusDone || c.status == nil {
		return
	}
	matchesHead := true
	switch c.head.Kind {
	case gitutil.GitHeadCommit:
		matchesHead = c.status.HeadCommitHash != nil && *c.status.HeadCommitHash == c.head.Commit
	case gitutil.GitHeadMissing:
		matchesHead = c.status.HeadCommitHash == nil
	}
	if !matchesHead {
		return
	}
	c.statusUsed = true
	if c.status.HeadCommitHash != nil {
		c.latestGitCommitHash = *c.status.HeadCommitHash
	} else {
		c.latestGitCommitHash = ""
	}
	hasChanges := c.status.HasChanges
	c.hasChanges = &hasChanges
}

func (c *memoryGitCollector) observeRemoteURLs(urls map[string]string) {
	if len(urls) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remotes == nil {
		c.remotes = make(map[string]string, len(urls))
	}
	for name, url := range urls {
		c.remotes[name] = url
	}
}

func (c *memoryGitCollector) snapshot() codexapi.ClientWorkspaceMetadata {
	c.mu.Lock()
	defer c.mu.Unlock()
	workspace := codexapi.ClientWorkspaceMetadata{LatestGitCommitHash: c.latestGitCommitHash}
	if len(c.remotes) > 0 {
		cloned := make(map[string]string, len(c.remotes))
		for name, url := range c.remotes {
			cloned[name] = url
		}
		workspace.AssociatedRemoteURLs = cloned
	}
	if c.hasChanges != nil {
		hasChanges := *c.hasChanges
		workspace.HasChanges = &hasChanges
	}
	return workspace
}

// memoryGitChangeProbe overlaps a positive-only worktree probe with request
// preparation; its observation is used only when it already completed and the
// caller never waits for it before sending the request (Rust #61939).
type memoryGitChangeProbe struct {
	results <-chan *gitutil.WorktreeProbe
	cancel  context.CancelFunc
}

func startMemoryGitChangeProbe(ctx context.Context, cwd string) *memoryGitChangeProbe {
	probeCtx, cancel := context.WithCancel(ctx)
	results := make(chan *gitutil.WorktreeProbe, 1)
	go func() { results <- gitutil.ProbeWorktreeChanges(probeCtx, cwd) }()
	return &memoryGitChangeProbe{results: results, cancel: cancel}
}

// completed returns the probe observation only if the probe already finished.
func (p *memoryGitChangeProbe) completed() *gitutil.WorktreeProbe {
	if p == nil {
		return nil
	}
	select {
	case observed := <-p.results:
		return observed
	default:
		return nil
	}
}

func (p *memoryGitChangeProbe) Close() {
	if p == nil || p.cancel == nil {
		return
	}
	p.cancel()
}
