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
	"codex_go/session"
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

// ---------------------------------------------------------------------------
// Shared task pinning (Rust #51500, "Add shared task pinning to the agent
// command center"). The dashboard core owns the pin order and the shortcut; the
// host owns the shared thread section, reached through thread/list filtered by
// the pinned section and thread/section/move.
// ---------------------------------------------------------------------------

// agentsOverviewPinRequestFunc issues one app-server JSON-RPC call for shared
// task pinning and reports the JSON-RPC error code alongside the error, so the
// caller can recognise a server without shared thread sections. It is the app
// layer's equivalent of the app server request handle Rust passes to
// list_pinned_threads / toggle_agents_overview_pin; the codextea.Options
// callbacks below are its only consumers.
type agentsOverviewPinRequestFunc func(method string, params any, result any) (rpcCode int, err error)

// agentsOverviewPinRequestOnClient issues one pin request on an app-server
// client the caller already holds. Rust #51500 hands list_pinned_threads and
// toggle_agents_overview_pin the request handle the dashboard refresh already
// uses; the command center's own listing goes through this adapter so the pins
// and the rows come from one connection.
func agentsOverviewPinRequestOnClient(ctx context.Context, client *remoteAppServerTUIClient) agentsOverviewPinRequestFunc {
	return func(method string, params any, result any) (int, error) {
		if client == nil {
			return 0, errors.New("app-server client is unavailable")
		}
		id, err := client.sendRequest(ctx, appserver.Method(method), params)
		if err != nil {
			return agentsOverviewPinRPCErrorCode(err), err
		}
		if err := client.waitResponse(ctx, id, result); err != nil {
			return agentsOverviewPinRPCErrorCode(err), err
		}
		return 0, nil
	}
}

// interactiveRemoteAgentsOverviewPinRequest binds a pin request to the remote
// app server. Each call opens a short-lived client like the other
// interactiveRemote handlers.
func interactiveRemoteAgentsOverviewPinRequest(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) agentsOverviewPinRequestFunc {
	return func(method string, params any, result any) (int, error) {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return 0, err
		}
		defer client.close()
		return agentsOverviewPinRequestOnClient(ctx, client)(method, params, result)
	}
}

// agentsOverviewPinRPCErrorCode reports the JSON-RPC code of a failed request
// (0 when the failure was not a JSON-RPC error, e.g. a transport failure).
func agentsOverviewPinRPCErrorCode(err error) int {
	var rpcErr *remoteRPCError
	if errors.As(err, &rpcErr) && rpcErr != nil {
		return rpcErr.Code
	}
	return 0
}

// agentsOverviewPinUnsupportedCode reports whether a server rejected shared
// thread sections. Rust's list_pinned_threads treats -32602..-32600 as "this
// server has no shared sections" and disables pinning instead of failing.
func agentsOverviewPinUnsupportedCode(code int) bool {
	return code <= -32600 && code >= -32602
}

// agentsOverviewPinPageSize mirrors Rust list_pinned_threads' page size.
const agentsOverviewPinPageSize = 100

// agentsOverviewPinnedSectionID is the shared "Pinned" section id
// (Rust PINNED_THREAD_SECTION_ID, Go session.PinnedThreadSectionID).
func agentsOverviewPinnedSectionID() *string {
	section := session.PinnedThreadSectionID
	return &section
}

// listAgentsOverviewPinnedThreads lists the shared pinned section in section
// order (Rust #51500 list_pinned_threads). The listing walks the pinned section
// for the default interactive sources and then for exec and app-server sources,
// so pinned tasks outside the recent-task window are discovered too, and reports
// supported=false when the server has no shared thread sections. The threads are
// returned whole because the command center seeds them into its rows as well as
// ranking them (Rust agents_overview_threads.rs).
func listAgentsOverviewPinnedThreads(request agentsOverviewPinRequestFunc) ([]appserver.Thread, bool, error) {
	if request == nil {
		return nil, false, nil
	}
	pinned := make([]appserver.Thread, 0, agentsOverviewPinPageSize)
	for _, kinds := range [][]appserver.ThreadSourceKind{
		// nil mirrors Rust's empty source-kind pass: the app server then
		// applies its default interactive sources (Atlas/ChatGPT included).
		nil,
		{appserver.ThreadSourceKindExec, appserver.ThreadSourceKindAppServer},
	} {
		var cursor *string
		for {
			limit := agentsOverviewPinPageSize
			archived := false
			params := appserver.ThreadListParams{
				Limit:          &limit,
				SortKey:        appserver.SortSectionPosition,
				Archived:       &archived,
				SourceKinds:    kinds,
				ModelProviders: []string{},
				UseStateDBOnly: true,
				// sectionId must be present (possibly null) for the server to
				// treat this as a section query.
				SectionID: appserver.OptionalString{Set: true, Value: agentsOverviewPinnedSectionID()},
			}
			if cursor != nil {
				params.Cursor = cursor
			}
			var response appserver.ThreadListResponse
			code, err := request(string(appserver.MethodThreadList), params, &response)
			if err != nil {
				if agentsOverviewPinUnsupportedCode(code) {
					return nil, false, nil
				}
				return nil, false, err
			}
			for i := range response.Data {
				thread := response.Data[i]
				if !agentsOverviewThreadPinnable(&thread) {
					continue
				}
				pinned = append(pinned, thread)
			}
			if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
				break
			}
			next := strings.TrimSpace(*response.NextCursor)
			cursor = &next
		}
	}
	return pinned, true, nil
}

// interactiveRemoteAgentsOverviewPinnedThreads adapts the shared pinned-section
// listing to the command center's pin callback (Rust #51500
// agents_overview.pinned_thread_ids).
func interactiveRemoteAgentsOverviewPinnedThreads(request agentsOverviewPinRequestFunc) codextea.AgentsOverviewPinnedThreadsFunc {
	return func() ([]string, bool, error) {
		threads, supported, err := listAgentsOverviewPinnedThreads(request)
		if err != nil || !supported {
			return nil, supported, err
		}
		pinned := make([]string, 0, len(threads))
		for i := range threads {
			if id := strings.TrimSpace(threads[i].ID); id != "" {
				pinned = append(pinned, id)
			}
		}
		return pinned, true, nil
	}
}

// agentsOverviewThreadPinnable mirrors Rust list_pinned_threads' filter: a root,
// non-ephemeral task (Rust is_overview_thread) whose thread source can carry a
// shared pin (Rust supports_shared_pinning).
func agentsOverviewThreadPinnable(thread *appserver.Thread) bool {
	if thread == nil || strings.TrimSpace(thread.ID) == "" {
		return false
	}
	if thread.Ephemeral {
		return false
	}
	if thread.ParentThreadID != nil && strings.TrimSpace(*thread.ParentThreadID) != "" {
		return false
	}
	return agentsoverview.SupportsSharedPinning(agentsOverviewRowSource(string(thread.Source)))
}

// interactiveRemoteAgentsOverviewTogglePin pins or unpins one task in the shared
// pinned section (Rust #51500 toggle_agents_overview_pin -> thread/section/move
// with PINNED_THREAD_SECTION_ID; unpinning clears the section).
func interactiveRemoteAgentsOverviewTogglePin(request agentsOverviewPinRequestFunc) codextea.AgentsOverviewTogglePinFunc {
	return func(threadID string, pinned bool) error {
		if request == nil {
			return errors.New("shared task pinning is unavailable in this runtime")
		}
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return errors.New("pinning a task requires a thread id")
		}
		params := appserver.ThreadSectionMoveParams{
			ThreadID: threadID,
			// Set with a nil value clears the section, which is how Rust
			// unpins (section_id: None).
			SectionID: appserver.OptionalString{Set: true},
		}
		if pinned {
			params.SectionID.Value = agentsOverviewPinnedSectionID()
		}
		if _, err := request(string(appserver.MethodThreadSectionMove), params, nil); err != nil {
			return err
		}
		return nil
	}
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
