package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/cli"
	"codex_go/session"
)

// runAgentsCommand mirrors Rust #39114 `codex agents`: open the shared agents
// overview against a local background app server or a server supplied with
// --remote, without creating a new session. On an interactive terminal the
// full-screen agents-overview dashboard (#39094/#39112) is opened; otherwise
// the active session list is rendered as a text overview.
func runAgentsCommand(ctx context.Context, opts *cli.AgentsOptions, root *cli.RootOptions, stdout io.Writer) error {
	return runAgentsCommandWithIO(ctx, opts, root, os.Stdin, stdout)
}

func runAgentsCommandWithIO(ctx context.Context, opts *cli.AgentsOptions, root *cli.RootOptions, stdin io.Reader, stdout io.Writer) error {
	remoteRoot := &cli.RootOptions{}
	if root != nil {
		*remoteRoot = *root
	}
	cwdOverride := ""
	if opts != nil {
		if strings.TrimSpace(opts.Remote) != "" {
			remoteRoot.Remote = opts.Remote
		}
		if strings.TrimSpace(opts.RemoteAuthEnv) != "" {
			remoteRoot.RemoteAuthEnv = opts.RemoteAuthEnv
		}
		cwdOverride = strings.TrimSpace(opts.Cwd)
	}
	if strings.TrimSpace(remoteRoot.Remote) != "" || strings.TrimSpace(remoteRoot.RemoteAuthEnv) != "" {
		endpoint, err := resolveInteractiveRemoteEndpoint(remoteRoot)
		if err != nil {
			return err
		}
		if shouldRunInteractiveTUI(stdin, stdout) {
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				return err
			}
			source := newRemoteAgentsDashboardSource(client, cwdOverride)
			defer source.Close()
			result, err := runAgentsDashboard(ctx, source, opts, stdin, stdout)
			if err != nil {
				return err
			}
			return writeAgentsOpenedSession(ctx, result, endpoint, stdout)
		}
		return runRemoteAgentsOverview(ctx, endpoint, stdout)
	}
	if shouldRunInteractiveTUI(stdin, stdout) {
		source, err := newAgentsDashboardSourceForLocal(ctx)
		if err != nil {
			return err
		}
		defer source.Close()
		result, err := runAgentsDashboard(ctx, source, opts, stdin, stdout)
		if err != nil {
			return err
		}
		return writeAgentsOpenedSession(ctx, result, nil, stdout)
	}
	return runLocalAgentsOverview(stdout)
}

func runLocalAgentsOverview(stdout io.Writer) error {
	return runLocalAgentsOverviewWithStore(newAgentsDashboardStore(), stdout)
}

func runLocalAgentsOverviewWithStore(store *session.Store, stdout io.Writer) error {
	if store == nil {
		return errors.New("session store is nil")
	}
	records, err := listSessionsByArchived(store, false)
	if err != nil {
		return err
	}
	writeAgentsOverviewHeader(stdout)
	for i := range records {
		name := strings.TrimSpace(records[i].Title)
		if name == "" {
			name = strings.TrimSpace(string(records[i].ID))
		}
		updated := records[i].UpdatedAt.UTC().Format(time.RFC3339)
		writeAgentsOverviewRow(stdout, string(records[i].ID), name, strings.TrimSpace(records[i].Metadata.Source), updated)
	}
	return nil
}

func runRemoteAgentsOverview(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, stdout io.Writer) error {
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		return err
	}
	defer client.close()
	params := remoteThreadListParams(&cli.SessionOptions{IncludeNonInteractive: true}, false, 100)
	var response appserver.ThreadListResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodThreadList, params, &response); err != nil {
		return err
	}
	writeAgentsOverviewHeader(stdout)
	for i := range response.Data {
		writeAgentsOverviewRow(stdout, response.Data[i].ID, remoteThreadDisplayName(&response.Data[i]), remoteThreadSource(string(response.Data[i].Source)), remoteThreadUpdated(response.Data[i].UpdatedAt))
	}
	return nil
}

func writeAgentsOverviewHeader(stdout io.Writer) {
	fmt.Fprintln(stdout, "Active sessions:")
	fmt.Fprintf(stdout, "%-40s %-32s %-14s %s\n", "ID", "Name", "Source", "Updated")
}

func writeAgentsOverviewRow(stdout io.Writer, id string, name string, source string, updated string) {
	fmt.Fprintf(stdout, "%-40s %-32s %-14s %s\n", id, name, source, updated)
}

func remoteThreadSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "unknown"
	}
	return source
}

func remoteThreadUpdated(updatedAt int64) string {
	return time.Unix(updatedAt, 0).UTC().Format(time.RFC3339)
}
