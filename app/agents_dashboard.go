package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/session"
	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/tui/markdown"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

// agentsDashboardResult is what the standalone dashboard hands back to the
// host after it closes.
type agentsDashboardResult struct {
	// OpenedThreadID is set when the user opened a root session or started a
	// new one. v1 hands the thread off to the host (summary + resume hint);
	// attaching the interactive session directly is the next increment.
	OpenedThreadID string
	// NewSession marks OpenedThreadID as a session this dashboard just started
	// in CWD, which has no rollout yet (Rust #45255).
	NewSession bool
	// CWD is the checkout a new session was started in.
	CWD string
}

// agentsDashboardSource is the data-source seam for the interactive dashboard.
// The remote implementation talks to an app server (daemon or --remote); the
// local fallback reads the session store directly.
type agentsDashboardSource interface {
	List(ctx context.Context) ([]agentsoverview.Row, error)
	// NewSession starts a blank session in cwd without sending a turn (Rust
	// #45255 opens a new session from the command center).
	NewSession(ctx context.Context, cwd string) (string, error)
	Stop(ctx context.Context, threadID string) error
	Rename(ctx context.Context, threadID, name string) error
	Archive(ctx context.Context, threadID string) error
	Delete(ctx context.Context, threadID string) error
	Close()
}

// newAgentsDashboardStore is the session-store hook for the local dashboard
// data path (overridden in tests).
var newAgentsDashboardStore = newSessionStore

// ---------------------------------------------------------------------------
// Row builders
// ---------------------------------------------------------------------------

// agentsOverviewRowsFromThreads builds dashboard rows from app-server threads,
// keeping only root sessions (Rust agents_overview: roots with subagent status
// folded into the root's group).
func agentsOverviewRowsFromThreads(threads []*appserver.Thread, currentThreadID string) []agentsoverview.Row {
	currentThreadID = strings.TrimSpace(currentThreadID)
	rows := make([]agentsoverview.Row, 0, len(threads))
	for _, thread := range threads {
		if thread == nil || strings.TrimSpace(thread.ID) == "" {
			continue
		}
		if thread.ParentThreadID != nil && strings.TrimSpace(*thread.ParentThreadID) != "" {
			continue // subagent thread: folded into the root row
		}
		row := agentsoverview.Row{
			ThreadID: strings.TrimSpace(thread.ID),
			Preview:  strings.TrimSpace(thread.Preview),
			CWD:      strings.TrimSpace(thread.CWD),
		}
		if thread.Model != nil {
			row.Model = strings.TrimSpace(*thread.Model)
		}
		if thread.Name != nil {
			row.Name = strings.TrimSpace(*thread.Name)
		}
		if thread.GitInfo != nil && thread.GitInfo.Branch != nil {
			row.GitBranch = strings.TrimSpace(*thread.GitInfo.Branch)
		}
		waitingApproval := false
		waitingUserInput := false
		for _, flag := range thread.Status.ActiveFlags {
			switch flag {
			case appserver.ThreadActiveFlagWaitingOnApproval:
				waitingApproval = true
			case appserver.ThreadActiveFlagWaitingOnUserInput:
				waitingUserInput = true
			}
		}
		row.Group = agentsoverview.GroupForStatus(thread.Status.Type, waitingApproval, waitingUserInput)
		row.StatusActive = strings.EqualFold(strings.TrimSpace(thread.Status.Type), "active")
		row.IsCurrent = strings.EqualFold(row.ThreadID, currentThreadID)
		rows = append(rows, row)
	}
	return rows
}

// agentsOverviewRowsFromRecords builds dashboard rows from local session-store
// records (the Windows/no-daemon fallback). Status comes from the last
// persisted rollout turn or the archived flag.
func agentsOverviewRowsFromRecords(records []session.Record, currentThreadID string) []agentsoverview.Row {
	currentThreadID = strings.TrimSpace(currentThreadID)
	rows := make([]agentsoverview.Row, 0, len(records))
	for i := range records {
		record := &records[i]
		if strings.TrimSpace(string(record.ID)) == "" {
			continue
		}
		if strings.TrimSpace(string(record.ParentThreadID)) != "" {
			continue // subagent thread: folded into the root row
		}
		row := agentsoverview.Row{
			ThreadID: strings.TrimSpace(string(record.ID)),
			Name:     strings.TrimSpace(record.Title),
			Preview:  strings.TrimSpace(record.Preview),
			CWD:      strings.TrimSpace(record.Metadata.CWD),
			Model:    strings.TrimSpace(record.Metadata.Model),
		}
		if branch, ok := record.Metadata.Git["branch"]; ok {
			row.GitBranch = strings.TrimSpace(branch)
		}
		// Local records only carry items when their history was materialized; the
		// dashboard listing skips history, so this is best-effort.
		row.LastMessage = lastAgentMessagePreviewFromSessionItems(record.Items)
		row.Group, row.StatusActive = localAgentGroupForRecord(record)
		row.IsCurrent = strings.EqualFold(strings.TrimSpace(string(record.ID)), currentThreadID)
		rows = append(rows, row)
	}
	return rows
}

func localAgentGroupForRecord(record *session.Record) (agentsoverview.Group, bool) {
	if record == nil {
		return agentsoverview.GroupFinished, false
	}
	if record.Archived {
		return agentsoverview.GroupFinished, false
	}
	if len(record.Metadata.RolloutTurns) == 0 {
		return agentsoverview.GroupReady, false
	}
	status := strings.ToLower(strings.TrimSpace(record.Metadata.RolloutTurns[len(record.Metadata.RolloutTurns)-1].Status))
	switch {
	case status == "inprogress" || status == "in_progress" || status == "running":
		return agentsoverview.GroupWorking, true
	case status == "error" || status == "failed" || strings.Contains(status, "error"):
		return agentsoverview.GroupNeedsYou, false
	default:
		return agentsoverview.GroupReady, false
	}
}

// ---------------------------------------------------------------------------
// Remote (app-server) source
// ---------------------------------------------------------------------------

type remoteAgentsDashboardSource struct {
	client      *remoteAppServerTUIClient
	cwdOverride string
}

func newRemoteAgentsDashboardSource(client *remoteAppServerTUIClient, cwdOverride string) *remoteAgentsDashboardSource {
	return &remoteAgentsDashboardSource{client: client, cwdOverride: strings.TrimSpace(cwdOverride)}
}

func (s *remoteAgentsDashboardSource) List(ctx context.Context) ([]agentsoverview.Row, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("app-server client is unavailable")
	}
	pageLimit := 100
	const maxThreads = 1000
	threadIDs := make([]string, 0, pageLimit)
	var cursor *string
	for len(threadIDs) < maxThreads {
		params := appserver.ThreadLoadedListParams{Limit: &pageLimit}
		if cursor != nil {
			params.Cursor = cursor
		}
		var response appserver.ThreadLoadedListResponse
		if err := remoteSessionRequest(ctx, s.client, appserver.MethodThreadLoadedList, params, &response); err != nil {
			return nil, err
		}
		threadIDs = append(threadIDs, response.Data...)
		if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
			break
		}
		next := strings.TrimSpace(*response.NextCursor)
		cursor = &next
	}
	// Rust agents_overview_threads seeds the command center with up to
	// RECENT_SESSION_LIMIT recent sessions in addition to loaded sessions. A
	// failed seed only logs: the dashboard still shows the loaded threads.
	recent, err := s.listRecentThreads(ctx)
	if err != nil {
		recent = nil
	}
	byID := make(map[string]*appserver.Thread, len(recent)+len(threadIDs))
	order := make([]string, 0, len(recent)+len(threadIDs))
	for _, thread := range recent {
		id := appserverThreadID(thread)
		if id == "" {
			continue
		}
		if _, exists := byID[id]; !exists {
			order = append(order, id)
		}
		byID[id] = thread
	}
	for _, threadID := range threadIDs {
		id := strings.TrimSpace(threadID)
		if id == "" {
			continue
		}
		if _, exists := byID[id]; !exists {
			order = append(order, id)
		}
	}
	threads := make([]*appserver.Thread, 0, len(order))
	for _, id := range order {
		if thread, ok := byID[id]; ok && thread != nil {
			threads = append(threads, thread)
			continue
		}
		thread, err := remoteThreadRead(ctx, s.client, id, false)
		if err == nil && thread != nil {
			threads = append(threads, thread)
		}
	}
	rows := agentsOverviewRowsFromThreads(threads, "")
	s.attachLastMessages(ctx, rows)
	return rows, nil
}

// recentSessionLimit seeds the agent command center with this many recent
// sessions, in addition to loaded sessions (Rust RECENT_SESSION_LIMIT, #46579).
const recentSessionLimit = 10

func appserverThreadID(thread *appserver.Thread) string {
	if thread == nil {
		return ""
	}
	return strings.TrimSpace(thread.ID)
}

// listRecentThreads mirrors Rust's recent seed: query interactive sessions and
// then exec/app-server sessions sorted by recency, merge, sort by recency, and
// truncate to recentSessionLimit.
func (s *remoteAgentsDashboardSource) listRecentThreads(ctx context.Context) ([]*appserver.Thread, error) {
	if s == nil || s.client == nil {
		return nil, nil
	}
	interactive, err := s.listRecentThreadsForSourceKinds(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Default interactive sources include Atlas/ChatGPT, which have no explicit
	// source kind; exec/app-server sessions need their own query.
	nonInteractive, err := s.listRecentThreadsForSourceKinds(ctx, []appserver.ThreadSourceKind{
		appserver.ThreadSourceKindExec,
		appserver.ThreadSourceKindAppServer,
	})
	if err != nil {
		return nil, err
	}
	recent := append(interactive, nonInteractive...)
	sort.SliceStable(recent, func(i, j int) bool {
		left, right := threadRecencyKey(recent[i]), threadRecencyKey(recent[j])
		if left != right {
			return left > right
		}
		return appserverThreadID(recent[i]) > appserverThreadID(recent[j])
	})
	if len(recent) > recentSessionLimit {
		recent = recent[:recentSessionLimit]
	}
	return recent, nil
}

func threadRecencyKey(thread *appserver.Thread) int64 {
	if thread == nil {
		return 0
	}
	if thread.RecencyAt != nil {
		return *thread.RecencyAt
	}
	return thread.UpdatedAt
}

// listRecentThreadsForSourceKinds pages thread/list by recency. An older server
// that rejects `recency_at` falls back to its activity-sorted history.
func (s *remoteAgentsDashboardSource) listRecentThreadsForSourceKinds(ctx context.Context, kinds []appserver.ThreadSourceKind) ([]*appserver.Thread, error) {
	limit := recentSessionLimit
	archived := false
	sortKey := appserver.SortRecencyAt
	recent := make([]*appserver.Thread, 0, limit)
	var cursor *string
	for len(recent) < limit {
		params := appserver.ThreadListParams{
			Limit:          &limit,
			SortKey:        sortKey,
			Archived:       &archived,
			SourceKinds:    kinds,
			ModelProviders: []string{},
			UseStateDBOnly: true,
		}
		if cursor != nil {
			params.Cursor = cursor
		}
		response, err := remoteThreadListWithCwdFallback(ctx, s.client, params)
		if err != nil {
			if sortKey == appserver.SortRecencyAt && recencySortUnsupportedError(err) {
				sortKey = appserver.SortUpdatedAt
				cursor = nil
				recent = recent[:0]
				continue
			}
			return nil, err
		}
		for index := range response.Data {
			thread := &response.Data[index]
			if thread.Ephemeral || (thread.ParentThreadID != nil && strings.TrimSpace(*thread.ParentThreadID) != "") {
				continue
			}
			if len(recent) >= limit {
				break
			}
			recent = append(recent, thread)
		}
		if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
			break
		}
		next := strings.TrimSpace(*response.NextCursor)
		cursor = &next
	}
	return recent, nil
}

// recencySortUnsupportedError matches an older server that does not understand
// the `recency_at` sort key (Rust's -32600/-32602 fallback).
func recencySortUnsupportedError(err error) bool {
	var rpc *remoteRPCError
	if !errors.As(err, &rpc) {
		return false
	}
	if rpc.Code != appserver.JSONRPCInvalidRequestErrorCode && rpc.Code != appserver.JSONRPCInvalidParamsErrorCode {
		return false
	}
	return strings.Contains(rpc.Message, "recency_at")
}

// attachLastMessages fills each task's details-pane "Last message" from the
// latest turn's final agent message (Rust agents_overview_threads reads
// thread/turns/list with limit 1; Rust fans the reads out, Go issues them on the
// dashboard's existing sequential read path).
func (s *remoteAgentsDashboardSource) attachLastMessages(ctx context.Context, rows []agentsoverview.Row) {
	if s == nil || s.client == nil {
		return
	}
	limit := 1
	for i := range rows {
		threadID := strings.TrimSpace(rows[i].ThreadID)
		if threadID == "" {
			continue
		}
		var page appserver.TurnsPage
		if err := remoteSessionRequest(ctx, s.client, appserver.MethodThreadTurnsList, appserver.ThreadTurnsListParams{
			ThreadID: threadID,
			Limit:    &limit,
		}, &page); err != nil {
			continue
		}
		if len(page.Data) == 0 {
			continue
		}
		rows[i].LastMessage = lastAgentMessagePreview(page.Data[0].Items)
	}
}

// lastAgentMessagePreview previews the latest agent message in a turn, matching
// Rust preview_agent_message (unwrap markdown fences, then bound the preview).
func lastAgentMessagePreview(items []appserver.ThreadItem) string {
	for i := len(items) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(items[i].Type), "agentMessage") {
			continue
		}
		if text := previewAgentMessage(items[i].Text); text != "" {
			return text
		}
	}
	return ""
}

func previewAgentMessage(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return agentsoverview.PreviewMarkdown(markdown.UnwrapMarkdownFences(text))
}

func lastAgentMessagePreviewFromSessionItems(items []session.Item) string {
	for i := len(items) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(items[i].Type), "agentMessage") {
			continue
		}
		if text := previewAgentMessage(items[i].Text); text != "" {
			return text
		}
	}
	return ""
}

// NewSession starts a thread in cwd with the destination's effective launch
// defaults and the managed new-thread defaults, without sending a turn (Rust
// #45255 new_agents_overview_session: `n` opens a blank session and leaves
// running agents alone).
func (s *remoteAgentsDashboardSource) NewSession(ctx context.Context, cwd string) (string, error) {
	started, err := s.StartSession(ctx, cwd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(started.Thread.ID), nil
}

// StartSession starts the thread and returns the full start response, which
// carries the settings the in-session command center attaches with.
func (s *remoteAgentsDashboardSource) StartSession(ctx context.Context, cwd string) (*appserver.ThreadStartResponse, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("app-server client is unavailable")
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = strings.TrimSpace(s.cwdOverride)
	}
	params := appserver.ThreadStartParams{}
	if cwd != "" {
		params.CWD = cwd
	}
	// Rust agents_overview.rs applies the destination's server defaults and the
	// managed new-thread defaults when creating a session (#43177/#43261/#44693).
	if defaults, layers, effective, ok := s.client.remoteNewThreadModelDefaults(ctx, params.CWD); ok {
		applyServerEffectiveLaunchDefaults(&params, effective, layers, nil, false, s.client.serverCatalogDefaultModel(ctx))
		applyManagedDefaultsToThreadStartParams(&params, s.client.state, defaults, layers, nil, false, false)
	}
	var started appserver.ThreadStartResponse
	if err := remoteSessionRequest(ctx, s.client, appserver.MethodThreadStart, params, &started); err != nil {
		return nil, err
	}
	if started.Thread == nil || strings.TrimSpace(started.Thread.ID) == "" {
		return nil, errors.New("thread start response did not include a thread id")
	}
	return &started, nil
}

func (s *remoteAgentsDashboardSource) Stop(ctx context.Context, threadID string) error {
	if s == nil || s.client == nil {
		return errors.New("app-server client is unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("agent thread id is required to stop")
	}
	thread, err := remoteThreadRead(ctx, s.client, threadID, true)
	if err != nil {
		return err
	}
	for i := range thread.Turns {
		if thread.Turns[i].Status == appserver.TurnStatusInProgress {
			var response turn.TurnInterruptResponse
			return remoteSessionRequest(ctx, s.client, appserver.MethodTurnInterrupt, turn.TurnInterruptParams{
				ThreadID: threadID,
				TurnID:   strings.TrimSpace(thread.Turns[i].ID),
			}, &response)
		}
	}
	return nil
}

func (s *remoteAgentsDashboardSource) Rename(ctx context.Context, threadID, name string) error {
	if s == nil || s.client == nil {
		return errors.New("app-server client is unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	name = strings.TrimSpace(name)
	if threadID == "" {
		return errors.New("agent thread id is required to rename")
	}
	if name == "" {
		return errors.New("task name must not be empty")
	}
	var response appserver.ThreadSetNameResponse
	return remoteSessionRequest(ctx, s.client, appserver.MethodThreadSetName, appserver.ThreadSetNameParams{
		ThreadID: threadID,
		Name:     name,
	}, &response)
}

// Archive archives the thread and its child agents on the app server
// (Rust #44433).
func (s *remoteAgentsDashboardSource) Archive(ctx context.Context, threadID string) error {
	if s == nil || s.client == nil {
		return errors.New("app-server client is unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("agent thread id is required to archive")
	}
	var response appserver.ThreadArchiveResponse
	return remoteSessionRequest(ctx, s.client, appserver.MethodThreadArchive, appserver.ThreadArchiveParams{ThreadID: threadID}, &response)
}

// Delete permanently deletes the thread and its child agents on the app server
// (Rust #44433).
func (s *remoteAgentsDashboardSource) Delete(ctx context.Context, threadID string) error {
	if s == nil || s.client == nil {
		return errors.New("app-server client is unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("agent thread id is required to delete")
	}
	var response appserver.ThreadDeleteResponse
	return remoteSessionRequest(ctx, s.client, appserver.MethodThreadDelete, appserver.ThreadDeleteParams{ThreadID: threadID}, &response)
}

func (s *remoteAgentsDashboardSource) Close() {
	if s != nil && s.client != nil {
		s.client.close()
	}
}

// ---------------------------------------------------------------------------
// Local (session-store) source
// ---------------------------------------------------------------------------

// localAgentsDashboardSource reads the local session store. It is the
// no-daemon fallback: listing and renaming work, while dispatch and stop
// require a running background app server or --remote.
type localAgentsDashboardSource struct {
	store *session.Store
}

func (s *localAgentsDashboardSource) List(ctx context.Context) ([]agentsoverview.Row, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("session store is unavailable")
	}
	records, err := listSessionsByArchived(s.store, false)
	if err != nil {
		return nil, err
	}
	return agentsOverviewRowsFromRecords(records, ""), nil
}

func (s *localAgentsDashboardSource) NewSession(ctx context.Context, cwd string) (string, error) {
	return "", errors.New("starting a session requires the background app server; start it with `codex app-server daemon start` or connect with `codex agents --remote`")
}

func (s *localAgentsDashboardSource) Stop(ctx context.Context, threadID string) error {
	return errors.New("stopping background tasks requires the background app server; start it with `codex app-server daemon start` or connect with `codex agents --remote`")
}

func (s *localAgentsDashboardSource) Archive(ctx context.Context, threadID string) error {
	return errors.New("archiving tasks requires the background app server; start it with `codex app-server daemon start` or connect with `codex agents --remote`")
}

func (s *localAgentsDashboardSource) Delete(ctx context.Context, threadID string) error {
	return errors.New("deleting tasks requires the background app server; start it with `codex app-server daemon start` or connect with `codex agents --remote`")
}

func (s *localAgentsDashboardSource) Rename(ctx context.Context, threadID, name string) error {
	if s == nil || s.store == nil {
		return errors.New("session store is unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	name = strings.TrimSpace(name)
	if threadID == "" {
		return errors.New("agent thread id is required to rename")
	}
	if name == "" {
		return errors.New("task name must not be empty")
	}
	_, err := s.store.UpdateMetadata(session.ThreadID(threadID), &session.MetadataPatch{Title: &name}, false)
	return err
}

func (s *localAgentsDashboardSource) Close() {}

// newAgentsDashboardSourceForLocal selects the local dashboard data source:
// the shared background app server is started when possible (Rust #39114) and
// used through its control socket; when the daemon is unavailable the session
// store fallback is used.
func newAgentsDashboardSourceForLocal(ctx context.Context) (agentsDashboardSource, error) {
	runner := appserverdaemon.NewLifecycleRunnerForCodexHome(auth.DefaultCodexHome(), "")
	if _, err := runner.Run(appserverdaemon.LifecycleStart); err == nil {
		endpoint := appserverdaemon.NewUnixSocketEndpoint(appserver.AppServerControlSocketPath(auth.DefaultCodexHome()))
		if client, err := openRemoteSessionClient(ctx, endpoint); err == nil {
			return newRemoteAgentsDashboardSource(client, ""), nil
		}
	}
	return &localAgentsDashboardSource{store: newAgentsDashboardStore()}, nil
}

// ---------------------------------------------------------------------------
// bubbletea dashboard model
// ---------------------------------------------------------------------------

type agentsDashboardListMsg struct {
	rows []agentsoverview.Row
	err  error
}

type agentsDashboardNewSessionMsg struct {
	threadID string
	cwd      string
	err      error
}

type agentsDashboardStopMsg struct {
	err error
}

type agentsDashboardRenameMsg struct {
	err error
}

type agentsDashboardLifecycleMsg struct {
	action string
	err    error
}

type agentsDashboardModel struct {
	ctx    context.Context
	view   *agentsoverview.View
	source agentsDashboardSource
	width  int
	height int
	notice string
	busy   bool
	result *agentsDashboardResult
	done   bool
	// pendingLifecycle holds "archive" or "delete" while the confirmation is
	// shown; the destructive action only runs after explicit confirmation
	// (Rust #44433).
	pendingLifecycle string
	pendingThreadID  string
	// pendingLifecycleArmed marks that the explicit confirmation required for
	// permanent deletion has been requested once (Rust #44744).
	pendingLifecycleArmed bool
	// keymap resolves the dashboard's shortcut bindings (Rust resolves the
	// command center's keys from the user's keymap).
	keymap *codextui.KeymapConfig
}

func newAgentsDashboardModel(ctx context.Context, source agentsDashboardSource, keymap *codextui.KeymapConfig, worktreesEnabled ...bool) *agentsDashboardModel {
	model := &agentsDashboardModel{
		ctx:    ctx,
		view:   agentsoverview.New(nil, "", true),
		source: source,
		keymap: keymap,
		width:  100,
		height: 24,
	}
	// Rust #43279: linked checkouts of one repository group together when the
	// worktrees feature is on and the session is local.
	if len(worktreesEnabled) > 0 && worktreesEnabled[0] {
		model.view.SetWorktreesEnabled(true)
	}
	return model
}

func (m *agentsDashboardModel) Init() bubbletea.Cmd {
	return m.refreshCmd()
}

func (m *agentsDashboardModel) refreshCmd() bubbletea.Cmd {
	if m == nil || m.source == nil {
		return nil
	}
	return func() bubbletea.Msg {
		rows, err := m.source.List(m.ctx)
		return agentsDashboardListMsg{rows: rows, err: err}
	}
}

func (m *agentsDashboardModel) Update(message bubbletea.Msg) (bubbletea.Model, bubbletea.Cmd) {
	if m == nil {
		return m, nil
	}
	switch msg := message.(type) {
	case bubbletea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		return m, nil
	case bubbletea.KeyMsg:
		return m, m.handleKey(msg)
	case agentsDashboardListMsg:
		if msg.err != nil {
			m.notice = "Failed to load shared agents: " + strings.TrimSpace(msg.err.Error())
		} else {
			m.notice = ""
			m.view.ApplyRefresh(msg.rows, m.view.SelectedThreadID())
		}
		m.busy = false
		return m, nil
	case agentsDashboardNewSessionMsg:
		if msg.err != nil {
			m.notice = "Failed to start session: " + strings.TrimSpace(msg.err.Error())
			m.busy = false
			return m, nil
		}
		m.busy = false
		if strings.TrimSpace(msg.threadID) == "" {
			m.notice = "Failed to start session: the server returned no thread id"
			return m, nil
		}
		// Rust #45255: the started session becomes the dashboard's result, and
		// the host opens it (no turn is sent).
		m.done = true
		m.result = &agentsDashboardResult{OpenedThreadID: strings.TrimSpace(msg.threadID), NewSession: true, CWD: strings.TrimSpace(msg.cwd)}
		return m, bubbletea.Quit
	case agentsDashboardStopMsg:
		if msg.err != nil {
			m.notice = "Failed to stop background task: " + strings.TrimSpace(msg.err.Error())
		} else {
			m.notice = ""
		}
		m.busy = false
		return m, m.refreshCmd()
	case agentsDashboardRenameMsg:
		if msg.err != nil {
			m.notice = "Failed to rename task: " + strings.TrimSpace(msg.err.Error())
		} else {
			m.notice = ""
		}
		m.busy = false
		return m, m.refreshCmd()
	case agentsDashboardLifecycleMsg:
		if msg.err != nil {
			label := "archive"
			if msg.action == "delete" {
				label = "delete"
			}
			m.notice = "Failed to " + label + " task: " + strings.TrimSpace(msg.err.Error())
		} else {
			m.notice = ""
		}
		m.busy = false
		return m, m.refreshCmd()
	default:
		return m, nil
	}
}

func (m *agentsDashboardModel) handleKey(msg bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.done {
		return nil
	}
	// Rust #44433: the archive/delete confirmation owns input until resolved,
	// with cancel as the safe default.
	if m.pendingLifecycle != "" {
		switch msg.String() {
		case "y", "enter":
			// Rust #44744: archiving acts on the first confirmation; permanent
			// deletion requires an explicit second confirmation.
			if m.pendingLifecycle == "delete" && !m.pendingLifecycleArmed {
				m.pendingLifecycleArmed = true
				return nil
			}
			action := m.pendingLifecycle
			threadID := m.pendingThreadID
			m.pendingLifecycle = ""
			m.pendingThreadID = ""
			m.pendingLifecycleArmed = false
			return m.lifecycleCmd(action, threadID)
		case "n", "esc":
			m.pendingLifecycle = ""
			m.pendingThreadID = ""
			m.pendingLifecycleArmed = false
			m.notice = "Cancelled"
		}
		return nil
	}
	switch msg.String() {
	case "up", "k":
		m.view.MoveSelection(false)
	case "down", "j":
		m.view.MoveSelection(true)
	case "pgup":
		m.view.PageUp()
	case "pgdown":
		m.view.PageDown()
	case "home":
		m.view.JumpTop()
	case "end":
		m.view.JumpBottom()
	case "enter":
		name := strings.TrimSpace(m.view.State.Input)
		switch action := m.view.Activate(); action {
		case agentsoverview.ActionRenameThread:
			return m.renameCmd(name)
		case agentsoverview.ActionOpenThread:
			m.done = true
			m.result = &agentsDashboardResult{OpenedThreadID: m.view.SelectedThreadID()}
			return bubbletea.Quit
		}
	case "esc":
		switch action := m.view.Cancel(); action {
		case agentsoverview.ActionExit:
			m.done = true
			return bubbletea.Quit
		}
	case "backspace":
		m.view.Backspace()
	case "ctrl+c":
		m.done = true
		return bubbletea.Quit
	}
	// Rust #45255: while the search or rename field owns the editor, plain
	// characters edit it instead of triggering a dashboard shortcut.
	if m.view.State.Searching || m.view.State.Renaming {
		if msg.Type == bubbletea.KeyRunes && !msg.Alt {
			for _, r := range msg.Runes {
				m.view.TypeChar(r)
			}
		}
		return nil
	}
	// Rust #45255: the dashboard shortcuts are single-letter bindings resolved
	// from the user's keymap.
	if m.agentKey("search", msg) && !m.view.State.Renaming {
		m.view.ToggleSearch()
	}
	if m.agentKey("new_task", msg) {
		return m.newSessionCmd()
	}
	if m.agentKey("rename", msg) {
		m.view.BeginRename()
	}
	if m.agentKey("stop", msg) {
		if action := m.view.StopSelected(); action == agentsoverview.ActionStopThread {
			return m.stopCmd(m.view.SelectedThreadID())
		}
	}
	if m.agentKey("archive", msg) {
		if action := m.view.ArchiveSelected(); action == agentsoverview.ActionArchiveThread {
			m.pendingLifecycle = "archive"
			m.pendingThreadID = m.view.SelectedThreadID()
			m.pendingLifecycleArmed = false
		}
	}
	if m.agentKey("delete", msg) {
		if action := m.view.DeleteSelected(); action == agentsoverview.ActionDeleteThread {
			m.pendingLifecycle = "delete"
			m.pendingThreadID = m.view.SelectedThreadID()
			m.pendingLifecycleArmed = false
		}
	}
	if m.agentKey("toggle_grouping", msg) {
		m.view.ToggleGrouping()
	}
	return nil
}

// agentKey reports whether the key event matches the resolved binding for one
// agents-dashboard action.
func (m *agentsDashboardModel) agentKey(action string, msg bubbletea.KeyMsg) bool {
	if m == nil {
		return false
	}
	spec := codextea.KeySpecFromKeyMsg(msg)
	if spec == "" {
		return false
	}
	return codextui.KeymapActionHasBinding(m.keymap, "agents", action, spec)
}

// newSessionCmd opens a new session in the selected checkout without sending a
// turn, so running agents keep running (Rust #45255 `n`).
func (m *agentsDashboardModel) newSessionCmd() bubbletea.Cmd {
	if m == nil || m.source == nil || m.busy {
		return nil
	}
	cwd := ""
	if m.view.State.Grouping == agentsoverview.GroupingProject {
		if row := m.view.SelectedRow(); row != nil {
			cwd = strings.TrimSpace(row.CWD)
		}
	}
	m.busy = true
	return func() bubbletea.Msg {
		threadID, err := m.source.NewSession(m.ctx, cwd)
		return agentsDashboardNewSessionMsg{threadID: threadID, cwd: cwd, err: err}
	}
}

func (m *agentsDashboardModel) renameCmd(name string) bubbletea.Cmd {
	if m == nil || m.source == nil || strings.TrimSpace(name) == "" {
		return nil
	}
	threadID := m.view.SelectedThreadID()
	if threadID == "" {
		return nil
	}
	m.busy = true
	return func() bubbletea.Msg {
		err := m.source.Rename(m.ctx, threadID, name)
		return agentsDashboardRenameMsg{err: err}
	}
}

func (m *agentsDashboardModel) stopCmd(threadID string) bubbletea.Cmd {
	if m == nil || m.source == nil || strings.TrimSpace(threadID) == "" || m.busy {
		return nil
	}
	m.busy = true
	return func() bubbletea.Msg {
		err := m.source.Stop(m.ctx, threadID)
		return agentsDashboardStopMsg{err: err}
	}
}

// lifecycleCmd runs a confirmed archive/delete against the dashboard source
// (Rust #44433).
func (m *agentsDashboardModel) lifecycleCmd(action string, threadID string) bubbletea.Cmd {
	if m == nil || m.source == nil || strings.TrimSpace(threadID) == "" || m.busy {
		return nil
	}
	if action == "delete" {
		m.notice = "Deleting task\u2026"
	} else {
		m.notice = "Archiving task\u2026"
	}
	m.busy = true
	return func() bubbletea.Msg {
		var err error
		if action == "delete" {
			err = m.source.Delete(m.ctx, threadID)
		} else {
			err = m.source.Archive(m.ctx, threadID)
		}
		return agentsDashboardLifecycleMsg{action: action, err: err}
	}
}

func (m *agentsDashboardModel) View() string {
	if m == nil || m.done {
		return ""
	}
	lines := m.view.RenderStyled(m.width, m.height)
	if m.pendingLifecycle != "" {
		verb := "Archive"
		if m.pendingLifecycle == "delete" {
			verb = "Permanently delete"
		}
		prompt := "  " + verb + " this task and its child agents? (y/n)"
		if m.pendingLifecycleArmed {
			prompt = "  Press y again to permanently delete this task and its child agents, or n to cancel."
		}
		lines = append(lines, prompt)
	}
	if m.notice != "" {
		lines = append(lines, "  "+m.notice)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Program runner
// ---------------------------------------------------------------------------

// runAgentsDashboard runs the interactive agents-overview dashboard until the
// user exits (esc) or opens a root session (enter on a row).
func runAgentsDashboard(ctx context.Context, source agentsDashboardSource, opts *cli.AgentsOptions, stdin io.Reader, stdout io.Writer, keymap *codextui.KeymapConfig, worktreesEnabled bool) (*agentsDashboardResult, error) {
	if source == nil {
		return nil, errors.New("agents dashboard source is unavailable")
	}
	model := newAgentsDashboardModel(ctx, source, keymap, worktreesEnabled)
	programOptions := []bubbletea.ProgramOption{bubbletea.WithInput(stdin), bubbletea.WithOutput(stdout)}
	if opts == nil || !opts.NoAltScreen {
		programOptions = append(programOptions, bubbletea.WithAltScreen())
	}
	program := bubbletea.NewProgram(model, programOptions...)
	result, err := program.Run()
	if err != nil {
		return nil, err
	}
	if finished, ok := result.(*agentsDashboardModel); ok && finished != nil {
		return finished.result, nil
	}
	return nil, nil
}

// writeAgentsOpenedSession prints the v1 hand-off summary after the dashboard
// opens a root session (interactive attach is the next increment).
func writeAgentsOpenedSession(ctx context.Context, result *agentsDashboardResult, endpoint *appserverdaemon.RemoteAppServerEndpoint, stdout io.Writer) error {
	if result == nil || strings.TrimSpace(result.OpenedThreadID) == "" {
		return nil
	}
	threadID := strings.TrimSpace(result.OpenedThreadID)
	// A session this dashboard just started has no rollout yet, so it is not
	// resumable until its first prompt (Rust #45255).
	if result.NewSession {
		cwd := strings.TrimSpace(result.CWD)
		if cwd == "" {
			cwd = "the selected checkout"
		}
		fmt.Fprintf(stdout, "Started a new session %s in %s.\n", threadID, cwd)
		fmt.Fprintln(stdout, "Send its first prompt to make it resumable.")
		return nil
	}
	name := ""
	if endpoint != nil {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err == nil {
			defer client.close()
			if thread, err := remoteThreadRead(ctx, client, threadID, false); err == nil {
				name = remoteThreadDisplayName(thread)
			}
		}
	} else if store := newAgentsDashboardStore(); store != nil {
		if record, err := store.Read(session.ThreadID(threadID), true, false); err == nil {
			name = strings.TrimSpace(record.Title)
		}
	}
	if name == "" {
		name = threadID
	}
	fmt.Fprintf(stdout, "Opened %s.\n", name)
	if endpoint != nil {
		fmt.Fprintf(stdout, "Resume it with `codex resume %s --remote %s`.\n", threadID, endpoint)
	} else {
		fmt.Fprintf(stdout, "Resume it with `codex resume %s`.\n", threadID)
	}
	return nil
}
