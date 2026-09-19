package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	agentsoverview "codex_go/tui/agents_overview"
	codextea "codex_go/tui/tea"
	"codex_go/worktree"
)

// interactiveRemoteAgentsOverviewRefresh lists loaded root sessions from the
// shared app server (Rust refresh_agents_overview_threads). Each call opens a
// short-lived client like the other interactiveRemote handlers.
func interactiveRemoteAgentsOverviewRefresh(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.AgentsOverviewRefreshFunc {
	return func(currentThreadID string) ([]agentsoverview.Row, error) {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		defer client.close()
		return newRemoteAgentsDashboardSource(client, "").List(ctx)
	}
}

// interactiveRemoteAgentsOverviewNewSession starts a blank session in the
// selected checkout without sending a turn, and returns the snapshot the
// dashboard attaches to (Rust #45255 new_agents_overview_session). The started
// session has no rollout, so the TUI reuses this snapshot until its first turn.
func interactiveRemoteAgentsOverviewNewSession(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.AgentsOverviewNewSessionFunc {
	return func(cwd string) (codextea.AgentThreadSwitchResponse, error) {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return codextea.AgentThreadSwitchResponse{}, err
		}
		defer client.close()
		started, err := newRemoteAgentsDashboardSource(client, "").StartSession(ctx, cwd)
		if err != nil {
			return codextea.AgentThreadSwitchResponse{}, err
		}
		return remoteTUIAgentSwitchResponseForStartedSession(started), nil
	}
}

// interactiveRemoteAgentsOverviewNewWorktree creates a managed worktree from
// the selected project's default branch and starts a blank session inside it
// (Rust #45276 new_agents_overview_worktree). A failed start removes the new
// checkout when it is clean, otherwise the error names the retained path.
func interactiveRemoteAgentsOverviewNewWorktree(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, root *cli.RootOptions) codextea.AgentsOverviewNewWorktreeFunc {
	return func(cwd string) (codextea.AgentThreadSwitchResponse, error) {
		cwd = strings.TrimSpace(cwd)
		if cwd == "" {
			return codextea.AgentThreadSwitchResponse{}, errors.New("a worktree session needs a source checkout")
		}
		if !interactiveRemoteEndpointIsLocal(endpoint) {
			return codextea.AgentThreadSwitchResponse{}, errors.New("managed worktrees require a local workspace")
		}
		settings, ok := interactiveWorktreeSettings(root)
		if !ok {
			return codextea.AgentThreadSwitchResponse{}, errors.New("managed worktrees require local worktree support")
		}
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return codextea.AgentThreadSwitchResponse{}, err
		}
		defer client.close()
		// Rust requires a trusted source project before creating its worktree.
		if status, statusErr := remoteProjectTrustStatus(ctx, client, cwd, cwd); statusErr == nil && status == TrustStatusUntrusted {
			return codextea.AgentThreadSwitchResponse{}, errors.New("the source project is not trusted")
		}
		manager := worktree.NewWorktreeManager(settings)
		base, err := worktree.DefaultWorktreeBase(cwd)
		if err != nil {
			return codextea.AgentThreadSwitchResponse{}, err
		}
		checkout, err := manager.Create(cwd, base)
		if err != nil {
			return codextea.AgentThreadSwitchResponse{}, err
		}
		started, err := newRemoteAgentsDashboardSource(client, "").StartSession(ctx, checkout.CWD)
		if err != nil {
			// Rust reports the start failure with its "Failed to start session: "
			// prefix before the retained-worktree cleanup.
			return codextea.AgentThreadSwitchResponse{}, retainedWorktreeSessionError(manager, checkout, fmt.Errorf("Failed to start session: %w", err))
		}
		// Rust rejects a session the server did not actually place in the new
		// checkout: the worktree would otherwise stay unattached.
		if started != nil && strings.TrimSpace(started.CWD) != "" && !worktree.PathsEqual(started.CWD, checkout.CWD) {
			return codextea.AgentThreadSwitchResponse{}, retainedWorktreeSessionError(manager, checkout, errors.New("The server did not apply the worktree directory."))
		}
		threadID := ""
		if started != nil && started.Thread != nil {
			threadID = strings.TrimSpace(started.Thread.ID)
		}
		if threadID == "" {
			return codextea.AgentThreadSwitchResponse{}, retainedWorktreeSessionError(manager, checkout, errors.New("the server returned no thread id"))
		}
		if err := manager.BindThread(checkout.Root, threadID); err != nil {
			return codextea.AgentThreadSwitchResponse{}, retainedWorktreeSessionError(manager, checkout, err)
		}
		return remoteTUIAgentSwitchResponseForStartedSession(started), nil
	}
}

// retainedWorktreeSessionError abandons a worktree whose session never started:
// a clean checkout is removed, and a checkout that cannot be removed is
// reported by path (Rust #45276's retained-worktree error).
func retainedWorktreeSessionError(manager *worktree.WorktreeManager, checkout worktree.ManagedWorktree, cause error) error {
	reason := strings.TrimSpace(cause.Error())
	if reason == "" {
		reason = "the worktree session could not be started"
	}
	// Rust's PendingWorktree drop removes only a clean checkout, so a checkout
	// with local changes is retained and reported by path.
	if removeErr := manager.RemoveManaged(checkout.SourceCWD, checkout.Root); removeErr == nil {
		return errors.New(reason)
	}
	return fmt.Errorf("%s A checkout was retained at %s; remove it with `git worktree remove <checkout-path>` from the source repository if it is no longer needed.", reason, checkout.Root)
}

func interactiveRemoteAgentsOverviewStop(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.AgentsOverviewStopFunc {
	return func(threadID string) error {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		return newRemoteAgentsDashboardSource(client, "").Stop(ctx, threadID)
	}
}

func interactiveRemoteAgentsOverviewRename(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.AgentsOverviewRenameFunc {
	return func(threadID string, name string) error {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		return newRemoteAgentsDashboardSource(client, "").Rename(ctx, threadID, name)
	}
}

func interactiveRemoteAgentsOverviewArchive(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.AgentsOverviewArchiveFunc {
	return func(threadID string) error {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		return newRemoteAgentsDashboardSource(client, "").Archive(ctx, threadID)
	}
}

func interactiveRemoteAgentsOverviewDelete(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.AgentsOverviewDeleteFunc {
	return func(threadID string) error {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		return newRemoteAgentsDashboardSource(client, "").Delete(ctx, threadID)
	}
}

// interactiveRemoteAgentsOverviewUsage reads the selected task's usage
// estimate through the app server's thread-scoped account/usage/read (Rust
// #44970 fetch_thread_usage -> ThreadUsageOutcome).
func interactiveRemoteAgentsOverviewUsage(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.AgentsOverviewUsageReaderFunc {
	return func(threadID string) (codextea.AgentsOverviewUsageResult, error) {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return codextea.AgentsOverviewUsageResult{}, errors.New("dashboard usage requires a thread id")
		}
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return codextea.AgentsOverviewUsageResult{}, err
		}
		defer client.close()
		scoped := threadID
		var response auth.GetAccountTokenUsageResponse
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodGetAccountTokenUsage, auth.GetAccountTokenUsageParams{ThreadID: &scoped}, &response); err != nil {
			return codextea.AgentsOverviewUsageResult{}, err
		}
		if response.ThreadUsage == nil {
			return codextea.AgentsOverviewUsageResult{Outcome: codextea.AgentsOverviewUsageDisabled}, nil
		}
		return codextea.AgentsOverviewUsageResult{
			Outcome: codextea.AgentsOverviewUsageAvailable,
			Usage:   agentsOverviewThreadUsageFromAuth(response.ThreadUsage),
		}, nil
	}
}

// agentsOverviewThreadUsageFromAuth maps the app-server's thread usage into the
// dashboard shape, summing only complete non-negative breakdown groups (Rust
// agents_overview_usage::usage_lines try_fold).
func agentsOverviewThreadUsageFromAuth(usage *auth.ThreadUsage) codextea.AgentsOverviewThreadUsage {
	out := codextea.AgentsOverviewThreadUsage{}
	if usage == nil {
		return out
	}
	out.ThreadID = strings.TrimSpace(usage.ThreadID)
	out.EstimatedCreditsMicros = usage.EstimatedUsageCreditsMicros
	if usage.EstimatedUsageUSDMicros != nil {
		usd := *usage.EstimatedUsageUSDMicros
		out.EstimatedUSDMicros = &usd
	}
	out.HasGroups = len(usage.Groups) > 0
	if len(usage.Groups) == 0 {
		return out
	}
	input, inputOK := int64(0), true
	output, outputOK := int64(0), true
	for _, group := range usage.Groups {
		if group.InputTokens == nil || *group.InputTokens < 0 {
			inputOK = false
		} else {
			input = saturatingAddInt64(input, *group.InputTokens)
		}
		if group.OutputTokens == nil || *group.OutputTokens < 0 {
			outputOK = false
		} else {
			output = saturatingAddInt64(output, *group.OutputTokens)
		}
	}
	if inputOK {
		out.GroupInputTokens = &input
	}
	if outputOK {
		out.GroupOutputTokens = &output
	}
	return out
}

func saturatingAddInt64(a int64, b int64) int64 {
	sum := a + b
	if sum < a {
		return math.MaxInt64
	}
	return sum
}

// interactiveStartAgentsDaemon starts the local background app server (Rust
// start_agents_daemon): it runs `codex app-server daemon start` through the
// daemon lifecycle runner, including the Windows pid-managed daemon.
func interactiveStartAgentsDaemon() error {
	runner := appserverdaemon.NewLifecycleRunnerForCodexHome(auth.DefaultCodexHome(), "")
	_, err := runner.Run(appserverdaemon.LifecycleStart)
	if err != nil {
		// Rust #46088: point users at `codex --no-daemon` when the agents
		// overview cannot start its shared server.
		return fmt.Errorf("%w\n%s", err, daemonOverviewHint)
	}
	return err
}
