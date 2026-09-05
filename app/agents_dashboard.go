package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/session"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/turn"
)

// agentsDashboardResult is what the standalone dashboard hands back to the
// host after it closes.
type agentsDashboardResult struct {
	// OpenedThreadID is set when the user pressed enter on a root session.
	// v1 hands the thread off to the host (summary + resume hint); attaching
	// the interactive session directly is the next increment.
	OpenedThreadID string
}

// agentsDashboardSource is the data-source seam for the interactive dashboard.
// The remote implementation talks to an app server (daemon or --remote); the
// local fallback reads the session store directly.
type agentsDashboardSource interface {
	List(ctx context.Context) ([]agentsoverview.Row, error)
	Dispatch(ctx context.Context, prompt, cwd string) (string, error)
	Stop(ctx context.Context, threadID string) error
	Rename(ctx context.Context, threadID, name string) error
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
		}
		if branch, ok := record.Metadata.Git["branch"]; ok {
			row.GitBranch = strings.TrimSpace(branch)
		}
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
	threads := make([]*appserver.Thread, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		thread, err := remoteThreadRead(ctx, s.client, threadID, false)
		if err == nil && thread != nil {
			threads = append(threads, thread)
		}
	}
	return agentsOverviewRowsFromThreads(threads, ""), nil
}

func (s *remoteAgentsDashboardSource) Dispatch(ctx context.Context, prompt, cwd string) (string, error) {
	if s == nil || s.client == nil {
		return "", errors.New("app-server client is unavailable")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("task prompt must not be empty")
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = strings.TrimSpace(s.cwdOverride)
	}
	params := appserver.ThreadStartParams{}
	if cwd != "" {
		params.CWD = cwd
	}
	var started appserver.ThreadStartResponse
	if err := remoteSessionRequest(ctx, s.client, appserver.MethodThreadStart, params, &started); err != nil {
		return "", err
	}
	threadID := ""
	if started.Thread != nil {
		threadID = strings.TrimSpace(started.Thread.ID)
	}
	if threadID == "" {
		return "", errors.New("thread start response did not include a thread id")
	}
	var turnStarted turn.TurnStartResponse
	if err := remoteSessionRequest(ctx, s.client, appserver.MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Input:    []turn.TurnUserInput{{Type: "text", Text: prompt}},
	}, &turnStarted); err != nil {
		return threadID, err
	}
	return threadID, nil
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

func (s *localAgentsDashboardSource) Dispatch(ctx context.Context, prompt, cwd string) (string, error) {
	return "", errors.New("dispatching background tasks requires the background app server; start it with `codex app-server daemon start` or connect with `codex agents --remote`")
}

func (s *localAgentsDashboardSource) Stop(ctx context.Context, threadID string) error {
	return errors.New("stopping background tasks requires the background app server; start it with `codex app-server daemon start` or connect with `codex agents --remote`")
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

type agentsDashboardDispatchMsg struct {
	threadID string
	err      error
}

type agentsDashboardStopMsg struct {
	err error
}

type agentsDashboardRenameMsg struct {
	err error
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
}

func newAgentsDashboardModel(ctx context.Context, source agentsDashboardSource) *agentsDashboardModel {
	return &agentsDashboardModel{
		ctx:    ctx,
		view:   agentsoverview.New(nil, "", true),
		source: source,
		width:  100,
		height: 24,
	}
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
	case agentsDashboardDispatchMsg:
		if msg.err != nil {
			m.notice = "Failed to start background task: " + strings.TrimSpace(msg.err.Error())
		} else if strings.TrimSpace(msg.threadID) != "" {
			m.notice = "Dispatched task " + msg.threadID
		}
		m.busy = false
		return m, m.refreshCmd()
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
	default:
		return m, nil
	}
}

func (m *agentsDashboardModel) handleKey(msg bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.done {
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
		prompt := strings.TrimSpace(m.view.State.Input)
		switch action := m.view.Activate(); action {
		case agentsoverview.ActionDispatchTask:
			return m.dispatchCmd(prompt)
		case agentsoverview.ActionRenameThread:
			return m.renameCmd(prompt)
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
	case "ctrl+f":
		m.view.ToggleSearch()
	case "ctrl+s":
		m.view.ToggleGrouping()
	case "ctrl+n":
		m.view.ClearNew()
	case "ctrl+r":
		m.view.BeginRename()
	case "ctrl+x":
		if action := m.view.StopSelected(); action == agentsoverview.ActionStopThread {
			return m.stopCmd(m.view.SelectedThreadID())
		}
	case "ctrl+c":
		m.done = true
		return bubbletea.Quit
	default:
		if msg.Type == bubbletea.KeyRunes {
			for _, r := range msg.Runes {
				m.view.TypeChar(r)
			}
		}
	}
	return nil
}

func (m *agentsDashboardModel) dispatchCmd(prompt string) bubbletea.Cmd {
	if m == nil || m.source == nil || prompt == "" || m.busy {
		return nil
	}
	cwd := ""
	if !m.view.State.StatusGrouping {
		if row := m.view.SelectedRow(); row != nil {
			cwd = strings.TrimSpace(row.CWD)
		}
	}
	m.busy = true
	return func() bubbletea.Msg {
		threadID, err := m.source.Dispatch(m.ctx, prompt, cwd)
		return agentsDashboardDispatchMsg{threadID: threadID, err: err}
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

func (m *agentsDashboardModel) View() string {
	if m == nil || m.done {
		return ""
	}
	lines := m.view.RenderStyled(m.width, m.height)
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
func runAgentsDashboard(ctx context.Context, source agentsDashboardSource, opts *cli.AgentsOptions, stdin io.Reader, stdout io.Writer) (*agentsDashboardResult, error) {
	if source == nil {
		return nil, errors.New("agents dashboard source is unavailable")
	}
	model := newAgentsDashboardModel(ctx, source)
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
