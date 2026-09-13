package app

import (
	"context"
	"errors"
	"math"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	agentsoverview "codex_go/tui/agents_overview"
	codextea "codex_go/tui/tea"
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
	return err
}
