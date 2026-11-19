package appserver

import (
	"context"
	"strings"
	"sync"

	"codex_go/codexapi"
	"codex_go/gitutil"
	"codex_go/turn"
)

// The Git observation helpers are variables so a test can hold the worktree
// scan open and prove that requests proceed without it.
var (
	turnGitDiscoverRoot = gitutil.DiscoverGitRoot
	turnGitRemoteURLs   = gitutil.GetGitRemoteURLs
	turnGitHeadState    = gitutil.GetHeadState
	turnGitHasChanges   = gitutil.HasChangesInRepo
)

// turnGitEnrichment mirrors Rust's TurnGitEnrichment (Rust #62437): one turn
// owns both its current Git snapshot and the background collection that
// publishes it. HEAD and the remote URLs are collected right after turn setup,
// while the worktree status scan waits until the turn is admitted to run, so a
// turn discarded during setup never pays for it.
type turnGitEnrichment struct {
	mu         sync.RWMutex
	workspaces map[string]codexapi.ClientWorkspaceMetadata
	admission  chan struct{}
	admitOnce  sync.Once
}

// startTurnGitEnrichment starts the background collection for one turn. The
// turn context bounds it: an interrupted turn stops the collection.
func startTurnGitEnrichment(ctx context.Context, cwd string) *turnGitEnrichment {
	if ctx == nil {
		ctx = context.Background()
	}
	enrichment := &turnGitEnrichment{
		workspaces: map[string]codexapi.ClientWorkspaceMetadata{},
		admission:  make(chan struct{}),
	}
	go enrichment.collect(ctx, strings.TrimSpace(cwd))
	return enrichment
}

func (e *turnGitEnrichment) collect(ctx context.Context, cwd string) {
	if cwd == "" {
		return
	}
	root, err := turnGitDiscoverRoot(ctx, cwd)
	if err != nil || strings.TrimSpace(root) == "" || ctx.Err() != nil {
		return
	}
	workspace := codexapi.ClientWorkspaceMetadata{
		AssociatedRemoteURLs: turnGitRemoteURLs(ctx, cwd),
	}
	if head := turnGitHeadState(ctx, cwd); head.Kind == gitutil.GitHeadCommit {
		workspace.LatestGitCommitHash = head.Commit
	}
	e.publish(root, workspace)

	select {
	case <-e.admission:
	case <-ctx.Done():
		return
	}
	hasChanges, err := turnGitHasChanges(ctx, cwd, root)
	if err != nil {
		return
	}
	workspace.HasChanges = &hasChanges
	e.publish(root, workspace)
}

func (e *turnGitEnrichment) publish(root string, workspace codexapi.ClientWorkspaceMetadata) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.workspaces == nil {
		e.workspaces = map[string]codexapi.ClientWorkspaceMetadata{}
	}
	e.workspaces[root] = workspace
}

// Admit releases the worktree scan. It is idempotent because a turn can be
// admitted through more than one runtime path.
func (e *turnGitEnrichment) Admit() {
	if e == nil {
		return
	}
	e.admitOnce.Do(func() { close(e.admission) })
}

// Snapshot returns the latest observation for the next model request. It is nil
// until the first collection publishes, so the metadata document simply omits
// the workspaces it does not know yet.
func (e *turnGitEnrichment) Snapshot() map[string]codexapi.ClientWorkspaceMetadata {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.workspaces) == 0 {
		return nil
	}
	out := make(map[string]codexapi.ClientWorkspaceMetadata, len(e.workspaces))
	for path, workspace := range e.workspaces {
		if len(workspace.AssociatedRemoteURLs) > 0 {
			remotes := make(map[string]string, len(workspace.AssociatedRemoteURLs))
			for name, url := range workspace.AssociatedRemoteURLs {
				remotes[name] = url
			}
			workspace.AssociatedRemoteURLs = remotes
		}
		if workspace.HasChanges != nil {
			observed := *workspace.HasChanges
			workspace.HasChanges = &observed
		}
		out[path] = workspace
	}
	return out
}

// turnGitEnrichmentCWD reports the working directory whose Git state the turn
// reports. Rust enriches only a freshly resolved single local environment
// (TurnEnvironments::single_local_environment_cwd), so a turn that targets
// several or remote environments reports no workspace.
func turnGitEnrichmentCWD(params *turn.TurnStartParams) (string, bool) {
	if params == nil {
		return "", false
	}
	switch len(params.Environments) {
	case 0:
		cwd := strings.TrimSpace(params.CWD)
		return cwd, cwd != ""
	case 1:
		environment := params.Environments[0]
		if kind, ok := environment["type"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(kind)) {
			case "", "local":
			default:
				return "", false
			}
		}
		cwd, _ := environment["cwd"].(string)
		if strings.TrimSpace(cwd) == "" {
			cwd = params.CWD
		}
		cwd = strings.TrimSpace(cwd)
		return cwd, cwd != ""
	default:
		return "", false
	}
}

// gitWorkspaces returns this turn's latest workspace observation for a model
// request, or nil when the turn reports no Git state yet.
func (a *activeRuntimeTurn) gitWorkspaces() map[string]codexapi.ClientWorkspaceMetadata {
	if a == nil || a.GitEnrichment == nil {
		return nil
	}
	return a.GitEnrichment.Snapshot()
}

// admitTurnGitEnrichment releases the turn's deferred worktree scan.
func (r *RuntimeRouter) admitTurnGitEnrichment(threadID string, turnID string) {
	if r == nil {
		return
	}
	r.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) {
		active.GitEnrichment.Admit()
	})
}
