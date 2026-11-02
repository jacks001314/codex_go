package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

// In-session command center composer (Rust #42428): the dashboard composes new
// tasks in the shared bottom-pane composer retained by the view, so drafts and
// editor state survive refreshes and reconnects.

// focusAgentsOverviewComposer mirrors Rust App::focus_composer: it syncs the
// live Vim setting onto the retained composer and gives it input focus.
func (m *Model) focusAgentsOverviewComposer() {
	if m == nil || m.agentsOverview == nil {
		return
	}
	m.agentsOverview.SetComposerVimEnabled(m.composerIsVimEnabled())
	if m.vimMode {
		m.agentsOverview.SetComposerVimInsert(m.vimInsert)
	}
	m.agentsOverview.FocusComposer()
}

// agentsOverviewComposerOffline mirrors Rust's connection_notice guard: a
// dashboard showing a connection or refresh error keeps editing available but
// never dispatches a task.
func (m *Model) agentsOverviewComposerOffline() bool {
	return m != nil && strings.TrimSpace(m.agentsOverviewNotice) != ""
}

// handleAgentsOverviewPasteEnter mirrors Rust ChatComposer::handle_paste_enter:
// while dispatch is paused an Enter press inserts a newline instead of sending.
func (m *Model) handleAgentsOverviewPasteEnter() {
	if m == nil || m.agentsOverview == nil {
		return
	}
	m.agentsOverview.InsertComposerText("\n")
}

// handleAgentsOverviewComposerPaste forwards a paste to the shared composer,
// focusing it first (Rust handle_paste).
func (m *Model) handleAgentsOverviewComposerPaste(text string) bool {
	if m == nil || m.agentsOverview == nil || text == "" {
		return false
	}
	m.focusAgentsOverviewComposer()
	return m.agentsOverview.InsertComposerText(text)
}

// handleAgentsOverviewComposerKey routes keys to the shared composer while it
// owns input (Rust AgentsOverviewView::handle_composer_key). Escape returns to
// the task list unless Vim insert mode or a popup owns it.
func (m *Model) handleAgentsOverviewComposerKey(msg bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil {
		return nil
	}
	view := m.agentsOverview
	keySpec := keySpecFromKeyMsg(msg)
	if msg.Paste && msg.Type == bubbletea.KeyRunes {
		m.handleAgentsOverviewComposerPaste(string(msg.Runes))
		return nil
	}
	if keySpec == "esc" && !view.ComposerOwnsEscape() {
		view.FocusList()
		return nil
	}
	// Vim normal mode keeps the small command subset the rename editor already
	// supports, so `/vim` behaves the same in the command center.
	if view.ComposerVimEnabled() && !view.ComposerVimInsert() && keySpec != "esc" {
		switch keySpec {
		case "i":
			view.SetComposerVimInsert(true)
		case "a":
			view.MoveComposerCursor(1)
			view.SetComposerVimInsert(true)
		case "shift-i", "0", "home":
			view.SetComposerCursor(0)
			if keySpec != "0" && keySpec != "home" {
				view.SetComposerVimInsert(true)
			}
		case "shift-a", "$", "end":
			view.SetComposerCursor(len(view.ComposerText()))
			if keySpec != "$" && keySpec != "end" {
				view.SetComposerVimInsert(true)
			}
		case "h", "left":
			view.MoveComposerCursor(-1)
		case "l", "right":
			view.MoveComposerCursor(1)
		case "x", "delete":
			view.ComposerDelete()
		}
		return nil
	}
	offline := m.agentsOverviewComposerOffline()
	submitBound := m.keyMatches("composer", "submit", keySpec)
	queueBound := m.keyMatches("composer", "queue", keySpec)
	if offline && (submitBound || queueBound || msg.Type == bubbletea.KeyEnter) {
		if msg.Type == bubbletea.KeyEnter {
			m.handleAgentsOverviewPasteEnter()
		}
		return nil
	}
	switch msg.Type {
	case bubbletea.KeyEnter:
		return m.submitAgentsOverviewComposer()
	case bubbletea.KeyEsc:
		if view.ComposerVimEnabled() && view.ComposerVimInsert() {
			view.SetComposerVimInsert(false)
			return nil
		}
		view.FocusList()
	case bubbletea.KeyBackspace:
		view.ComposerBackspace()
	case bubbletea.KeyDelete:
		view.ComposerDelete()
	case bubbletea.KeyLeft:
		view.MoveComposerCursor(-1)
	case bubbletea.KeyRight:
		view.MoveComposerCursor(1)
	case bubbletea.KeyHome, bubbletea.KeyCtrlA:
		view.SetComposerCursor(0)
	case bubbletea.KeyEnd, bubbletea.KeyCtrlE:
		view.SetComposerCursor(len(view.ComposerText()))
	case bubbletea.KeyCtrlJ:
		view.InsertComposerText("\n")
	case bubbletea.KeySpace:
		view.InsertComposerRune(' ')
	case bubbletea.KeyRunes:
		if !msg.Alt {
			for _, r := range msg.Runes {
				view.InsertComposerRune(r)
			}
		}
	}
	return nil
}

// submitAgentsOverviewComposer mirrors Rust's InputResult::Submitted arm: the
// draft is dispatched verbatim, whitespace included (trim_submission = false).
func (m *Model) submitAgentsOverviewComposer() bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil {
		return nil
	}
	submission, ok := m.agentsOverview.TakeComposerSubmission()
	if !ok || strings.TrimSpace(submission.Text) == "" {
		return nil
	}
	return m.dispatchAgentsOverviewTaskCmd(submission.Text)
}

// dispatchAgentsOverviewTaskCmd starts a task in the selected checkout and
// sends the composed prompt (Rust AppEvent::DispatchAgentsOverviewTask).
func (m *Model) dispatchAgentsOverviewTaskCmd(prompt string) bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil {
		return nil
	}
	fail := func(notice string) bubbletea.Cmd {
		if notice != "" {
			m.agentsOverviewNotice = notice
		}
		m.restoreAgentsOverviewPrompt(prompt)
		return nil
	}
	if m.agentsOverviewBusy {
		return fail("")
	}
	if m.onAgentsOverviewNewSession == nil {
		return fail("The agents dashboard cannot create tasks in this runtime.")
	}
	if strings.TrimSpace(m.pendingServerProfile) != "" {
		return fail("Wait for permissions to update before switching tasks.")
	}
	if m.agentsOverviewComposerOffline() {
		return fail("")
	}
	cwd := ""
	if m.agentsOverview.State.Grouping != agentsoverview.GroupingStatus {
		if row := m.agentsOverview.SelectedRow(); row != nil {
			cwd = strings.TrimSpace(row.CWD)
		}
	}
	m.agentsOverviewBusy = true
	m.agentsOverviewPendingDispatch = prompt
	m.agentsOverviewAttachments = cloneComposerAttachments(m.attachments)
	return func() bubbletea.Msg {
		response, err := m.onAgentsOverviewNewSession(cwd)
		return agentsOverviewNewSessionMsg{response: response, err: err}
	}
}

// restoreAgentsOverviewPrompt mirrors Rust restore_agents_overview_prompt: a
// failed dispatch puts the prompt back into an empty composer; otherwise the
// text is surfaced in the transcript so it is not lost.
func (m *Model) restoreAgentsOverviewPrompt(prompt string) {
	if m == nil || strings.TrimSpace(prompt) == "" {
		return
	}
	if m.agentsOverview != nil && m.agentsOverview.RestoreComposerPrompt(prompt) {
		return
	}
	m.addInfoHistoryMessage("Unsent task: " + prompt)
	m.refreshTranscript()
}

// agentsOverviewListKeyBound reports whether key already drives a list or
// agents action, so plain typing does not steal a configured shortcut (Rust's
// shortcut keys win over composer focus in the same way).
func (m *Model) agentsOverviewListKeyBound(key string) bool {
	if m == nil || key == "" {
		return false
	}
	for _, action := range codextui.KeymapActions(codextui.KeymapActionFilter{}) {
		if action.Context != "list" && action.Context != "agents" {
			continue
		}
		if codextui.KeymapActionHasBinding(m.keymapConfig, action.Context, action.Action, key) {
			return true
		}
	}
	return false
}
