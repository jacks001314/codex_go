package tea

// Rust #61619: `/archive` hides the current task and opens the command center
// before the server answers. The archive request runs in the background; the
// displayed conversation, its draft, and its approvals survive until the
// request resolves. A failure makes the task visible again and keeps its draft.

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

// currentArchiveExitWait bounds how long shutdown waits for outstanding
// archive requests before reporting them as unconfirmed (Rust: five seconds).
var currentArchiveExitWait = 5 * time.Second

type pendingCurrentArchive struct {
	selection codextui.SessionSelection
	threadID  string
	done      chan struct{}
	err       error

	// Side-chat cleanup runs before the archive request, so the task stays
	// displayed until every side chat is closed.
	cleaning      bool
	sides         []*activeSideConversation
	sideRemaining int
	sideErr       error
}

// currentArchiveSideCleanupMsg reports one closed side chat.
type currentArchiveSideCleanupMsg struct {
	threadID     string
	sideThreadID string
	err          error
}

// currentThreadArchiveMsg reports the background archive request's outcome.
type currentThreadArchiveMsg struct {
	threadID string
}

// archiveWaitFinishedMsg carries shutdown warnings for unconfirmed archives.
type archiveWaitFinishedMsg struct {
	warnings []string
}

// beginCurrentThreadArchive starts archiving the displayed task. Side chats are
// stopped first; the task is only detached once cleanup succeeds.
func (m *Model) beginCurrentThreadArchive(selection codextui.SessionSelection) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID := strings.TrimSpace(selection.Target.ThreadID)
	if threadID == "" || m.currentThreadArchivePending(threadID) {
		return nil
	}
	// Rust keeps the draft and approvals until the archive resolves, so the
	// composer is captured before the task is detached.
	m.agentsOverviewNotice = ""
	m.agentsOverviewArchiveError = ""
	m.captureAgentsOverviewDraft(threadID)
	if owner := strings.TrimSpace(m.VoiceConversation.ThreadID); owner == threadID && m.VoiceConversation.Running() {
		m.stopVoiceConversation()
	}
	sides := m.currentArchiveSideConversations(threadID)
	if len(sides) == 0 || m.onCloseSide == nil {
		return m.startPendingCurrentArchive(selection)
	}
	record := &pendingCurrentArchive{
		selection:     selection,
		threadID:      threadID,
		done:          make(chan struct{}),
		cleaning:      true,
		sides:         sides,
		sideRemaining: len(sides),
	}
	if m.pendingCurrentArchives == nil {
		m.pendingCurrentArchives = map[string]*pendingCurrentArchive{}
	}
	m.pendingCurrentArchives[threadID] = record
	// Rust paints "Archiving…" before waiting for side chat cleanup.
	m.footerHintOverride = "Archiving\u2026"
	m.refreshTranscript()
	closer := m.onCloseSide
	commands := make([]bubbletea.Cmd, 0, len(sides))
	for _, side := range sides {
		params := SideCloseParams{
			ParentThreadID: strings.TrimSpace(side.ParentThreadID),
			SideThreadID:   strings.TrimSpace(side.SideThreadID),
		}
		commands = append(commands, func() bubbletea.Msg {
			_, err := closer(params)
			return currentArchiveSideCleanupMsg{threadID: threadID, sideThreadID: params.SideThreadID, err: err}
		})
	}
	return bubbletea.Batch(commands...)
}

// currentArchiveSideConversations lists the side chats owned by a task.
func (m *Model) currentArchiveSideConversations(threadID string) []*activeSideConversation {
	if m == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	seen := map[*activeSideConversation]bool{}
	sides := make([]*activeSideConversation, 0, 1)
	add := func(side *activeSideConversation) {
		if side == nil || seen[side] || strings.TrimSpace(side.SideThreadID) == "" {
			return
		}
		seen[side] = true
		sides = append(sides, side)
	}
	for _, side := range m.sideConversations {
		if side != nil && strings.TrimSpace(side.ParentThreadID) == threadID {
			add(side)
		}
	}
	for _, side := range m.sidebarTabs[threadID] {
		add(side)
	}
	return sides
}

// applyCurrentArchiveSideCleanup finishes one side chat close before starting
// the archive request.
func (m *Model) applyCurrentArchiveSideCleanup(msg currentArchiveSideCleanupMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	record := m.pendingCurrentArchives[msg.threadID]
	if record == nil || !record.cleaning {
		return nil
	}
	if record.sideRemaining > 0 {
		record.sideRemaining--
	}
	if msg.err != nil && record.sideErr == nil {
		record.sideErr = msg.err
	}
	if record.sideRemaining > 0 {
		return nil
	}
	m.footerHintOverride = ""
	m.refreshTranscript()
	if record.sideErr != nil {
		delete(m.pendingCurrentArchives, msg.threadID)
		m.notice = "Failed to archive current thread: " + strings.TrimSpace(record.sideErr.Error())
		m.refreshTranscript()
		return nil
	}
	m.dropArchivedSideConversations(record.sides)
	return m.startPendingCurrentArchive(record.selection)
}

// dropArchivedSideConversations forgets the cleaned side chats now that the
// task is being archived.
func (m *Model) dropArchivedSideConversations(sides []*activeSideConversation) {
	if m == nil {
		return
	}
	for _, side := range sides {
		if side == nil {
			continue
		}
		if m.activeSide == side {
			m.activeSide = nil
			m.resetSidePaneViewState()
			m.sideStartPending = false
		}
		m.forgetRetainedSideConversation(side)
		m.unregisterSidebarTab(side)
		m.abandonSideThread(side.SideThreadID)
	}
}

// startPendingCurrentArchive detaches the displayed task, opens the command
// center with that task hidden, and starts the archive request in the
// background.
func (m *Model) startPendingCurrentArchive(selection codextui.SessionSelection) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID := strings.TrimSpace(selection.Target.ThreadID)
	if threadID == "" {
		return nil
	}
	record := &pendingCurrentArchive{
		selection: selection,
		threadID:  threadID,
		done:      make(chan struct{}),
	}
	if m.pendingCurrentArchives == nil {
		m.pendingCurrentArchives = map[string]*pendingCurrentArchive{}
	}
	m.pendingCurrentArchives[threadID] = record
	m.footerHintOverride = ""
	m.noteThreadHandoff(threadID, "")
	m.activeSide = nil
	m.sideStartPending = false
	m.switchThreadID("")
	if m.State != nil {
		m.State.Messages = nil
		m.State.BumpMessagesRevision()
		m.State.SetThreadName("")
		m.setStatus("idle")
	}
	m.refreshTranscript()
	overview := m.applyAgentsCommand()
	if m.agentsOverview != nil {
		m.agentsOverview.PrepareRemoval(map[string]struct{}{threadID: {}})
		m.agentsOverview.HideThread(threadID)
	}
	m.refreshTranscript()
	archive := func() bubbletea.Msg {
		_, err := m.runSessionAction(selection)
		record.err = err
		close(record.done)
		return currentThreadArchiveMsg{threadID: threadID}
	}
	return bubbletea.Batch(overview, archive)
}

// applyCurrentThreadArchive finishes an archive request. Success removes the
// task and its draft; failure shows the task and its preserved draft again.
func (m *Model) applyCurrentThreadArchive(msg currentThreadArchiveMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	record := m.pendingCurrentArchives[msg.threadID]
	if record == nil {
		return nil
	}
	delete(m.pendingCurrentArchives, msg.threadID)
	if record.err != nil {
		// Reads started before the failed mutation can carry stale state.
		m.cancelAgentsOverviewRefresh(m.backendID)
		if m.agentsOverview != nil {
			m.agentsOverview.UnhideThread(msg.threadID)
		}
		m.agentsOverviewNotice = "Failed to archive thread " + msg.threadID + ": " + strings.TrimSpace(record.err.Error())
		m.agentsOverviewArchiveError = m.agentsOverviewNotice
		m.refreshTranscript()
		return m.refreshAgentsOverviewCmd()
	}
	m.agentsOverviewNotice = ""
	m.agentsOverviewArchiveError = ""
	m.noteAgentsOverviewRemovedThread(msg.threadID)
	if m.agentsOverviewRemoved == nil {
		m.agentsOverviewRemoved = map[string]bool{}
	}
	m.agentsOverviewRemoved[msg.threadID] = true
	m.clearAgentsOverviewBlankSession(msg.threadID)
	m.dropPendingSteersForThread(msg.threadID)
	m.setSessionArchived(msg.threadID, true)
	delete(m.agentsOverviewDrafts, msg.threadID)
	delete(m.agentsOverviewDraftShellModes, msg.threadID)
	delete(m.agentsOverviewDraftPastes, msg.threadID)
	delete(m.dynamicToolThreads, msg.threadID)
	if m.agentsOverview != nil {
		m.agentsOverview.HideThread(msg.threadID)
	}
	agentCmd := m.setAgentPickerThreadVisibility(msg.threadID, true)
	m.cancelAgentsOverviewRefresh(m.backendID)
	refreshCmd := m.refreshAgentsOverviewCmd()
	if agentCmd == nil {
		return refreshCmd
	}
	if refreshCmd == nil {
		return agentCmd
	}
	return bubbletea.Batch(agentCmd, refreshCmd)
}

// currentThreadArchivePending reports whether a task is still being archived.
func (m *Model) currentThreadArchivePending(threadID string) bool {
	if m == nil || len(m.pendingCurrentArchives) == 0 {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	_, ok := m.pendingCurrentArchives[threadID]
	return ok
}

// waitForPendingArchives reports failures and unconfirmed archives within the
// shutdown budget (Rust App::finish_pending_archives).
func (m *Model) waitForPendingArchivesCmd(pending []*pendingCurrentArchive) bubbletea.Cmd {
	return func() bubbletea.Msg {
		deadline := time.After(currentArchiveExitWait)
		warnings := make([]string, 0, len(pending))
		for _, record := range pending {
			if record == nil {
				continue
			}
			select {
			case <-record.done:
				if record.err != nil {
					warnings = append(warnings, "Failed to archive thread "+record.threadID+": "+strings.TrimSpace(record.err.Error()))
				}
			case <-deadline:
				warnings = append(warnings, "Archive of thread "+record.threadID+" was not confirmed.")
			}
		}
		sort.Strings(warnings)
		return archiveWaitFinishedMsg{warnings: warnings}
	}
}

// quitCmd quits immediately unless an archive request is in flight. Closing a
// connection can abandon that request, so shutdown waits briefly and reports
// the outcome (Rust waits up to five seconds before exiting).
func (m *Model) quitCmd() bubbletea.Cmd {
	if m == nil {
		return bubbletea.Quit
	}
	m.closeClipboardWorker()
	pending := make([]*pendingCurrentArchive, 0, len(m.pendingCurrentArchives))
	for _, record := range m.pendingCurrentArchives {
		if record != nil && !record.cleaning {
			pending = append(pending, record)
		}
	}
	if len(pending) == 0 {
		return bubbletea.Quit
	}
	return m.waitForPendingArchivesCmd(pending)
}

func (m *Model) applyArchiveWaitFinished(msg archiveWaitFinishedMsg) bubbletea.Cmd {
	for _, warning := range msg.warnings {
		if warning = strings.TrimSpace(warning); warning != "" {
			fmt.Fprintln(os.Stderr, warning)
		}
	}
	return bubbletea.Quit
}
