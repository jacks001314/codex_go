package app

import (
	"context"

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

func interactiveRemoteAgentsOverviewDispatch(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.AgentsOverviewDispatchFunc {
	return func(prompt string, cwd string) (string, error) {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return "", err
		}
		defer client.close()
		return newRemoteAgentsDashboardSource(client, "").Dispatch(ctx, prompt, cwd)
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

// interactiveStartAgentsDaemon starts the local background app server (Rust
// start_agents_daemon): it runs `codex app-server daemon start` through the
// daemon lifecycle runner, including the Windows pid-managed daemon.
func interactiveStartAgentsDaemon() error {
	runner := appserverdaemon.NewLifecycleRunnerForCodexHome(auth.DefaultCodexHome(), "")
	_, err := runner.Run(appserverdaemon.LifecycleStart)
	return err
}
