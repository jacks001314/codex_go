package tea

import (
	"io"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// Rust parity: codex-rs/tui/src/chatwidget/status_surfaces.rs (through #54114).
//
// The program status reports the displayed thread's actual state independently
// of terminal-title settings. Replays never produce an unseen result, and a
// completed or failed turn is retained until deliberate input acknowledges it.

// programStatusBlockedKind classifies interactive requests that prevent the
// agent from continuing, including requests hidden behind another view.
func (m *Model) programStatusBlockedKind() (codextui.ProgramStatusKind, bool) {
	if m == nil {
		return "", false
	}
	if modal := m.modal; modal != nil {
		switch modal.kind {
		case ModalKindApproval:
			return codextui.ProgramStatusKindPermission, true
		case ModalKindElicitation:
			if kind, ok := appLinkProgramStatusKind(modal.appLink); ok {
				return kind, true
			}
			// An MCP elicitation form asks the user a question; an approval
			// action reuses the same overlay for permission.
			return codextui.ProgramStatusKindQuestion, true
		case ModalKindUserInput:
			return codextui.ProgramStatusKindQuestion, true
		}
	}
	// Async questions only block once the agent is no longer running, so a
	// long-running turn does not look stuck behind its own questions.
	if !m.isTaskRunning() && m.asyncQuestions.UnansweredCount() > 0 {
		return codextui.ProgramStatusKindQuestion, true
	}
	return "", false
}

// appLinkProgramStatusKind mirrors the tool-suggestion classification of Rust
// bottom_pane::app_link_view::AppLinkView::program_status_blocked_kind.
func appLinkProgramStatusKind(view *bottompane.AppLinkView) (codextui.ProgramStatusKind, bool) {
	if view == nil || !view.TerminalTitleRequiresAction() {
		return "", false
	}
	suggestionType := ""
	if view.SuggestionType != nil {
		suggestionType = string(*view.SuggestionType)
	}
	switch suggestionType {
	case string(bottompane.AppLinkSuggestionAuth):
		return codextui.ProgramStatusKindAuth, true
	case string(bottompane.AppLinkSuggestionInstall), string(bottompane.AppLinkSuggestionEnable):
		return codextui.ProgramStatusKindPermission, true
	default:
		return codextui.ProgramStatusKindQuestion, true
	}
}

// desiredProgramStatus is the lifecycle state reported to the terminal.
func (m *Model) desiredProgramStatus() codextui.ProgramStatus {
	if m == nil {
		return codextui.ProgramStatus{}
	}
	if kind, ok := m.programStatusBlockedKind(); ok {
		return codextui.BlockedProgramStatus(kind)
	}
	if m.isTaskRunning() ||
		m.isUserTurnPendingOrRunning() ||
		len(m.pendingSteers) > 0 ||
		len(m.commandLifecycle.UnifiedExecProcesses) > 0 ||
		(m.programStatusAfterTurn.State == codextui.ProgramStatusDone && m.goalIsActive()) {
		return codextui.ProgramStatus{State: codextui.ProgramStatusWorking}
	}
	return m.programStatusAfterTurn
}

// goalIsActive reports whether the displayed thread has a goal the runtime will
// continue after the current turn.
func (m *Model) goalIsActive() bool {
	return m != nil && m.currentGoal != nil && m.currentGoal.Status == appserver.GoalActive
}

// acknowledgeProgramStatus clears an unseen completed or failed result. Active
// work and blocking prompts take precedence, so they are left alone.
func (m *Model) acknowledgeProgramStatus() {
	if m == nil {
		return
	}
	switch m.desiredProgramStatus().State {
	case codextui.ProgramStatusDone, codextui.ProgramStatusError:
		m.programStatusAfterTurn = codextui.ProgramStatus{}
	}
}

// acknowledgeProgramStatusOnInput applies Rust #54114's input acknowledgment:
// deliberate keyboard, paste, and mouse input clears a retained result, while
// bare pointer movement does not.
func (m *Model) acknowledgeProgramStatusOnInput(message bubbletea.Msg) {
	switch msg := message.(type) {
	case bubbletea.KeyMsg:
		if msg.Paste && len(msg.Runes) == 0 {
			return
		}
		m.acknowledgeProgramStatus()
	case bubbletea.MouseMsg:
		if msg.Action == bubbletea.MouseActionMotion {
			return
		}
		m.acknowledgeProgramStatus()
	}
}

// itermSessionDetail is the bounded activity text carried by OSC 21337. Only a
// running turn has a detail, and the generic "Working" header is dropped.
func (m *Model) itermSessionDetail(status codextui.ProgramStatus) string {
	if m == nil || status.State != codextui.ProgramStatusWorking || !m.isTaskRunning() {
		return ""
	}
	detail := strings.TrimSpace(m.workingStatusHeader)
	if detail == "Working" {
		return ""
	}
	return detail
}

// refreshTerminalProgramStatusCmd emits the current lifecycle state, or nothing
// when the terminal does not support the protocol or nothing changed.
func (m *Model) refreshTerminalProgramStatusCmd() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	status := m.desiredProgramStatus()
	sequence := codextui.SetTerminalProgramStatus(status, m.itermSessionDetail(status))
	if sequence == "" {
		return nil
	}
	return m.programStatusWriter(sequence)
}

func (m *Model) clearTerminalProgramStatusCmd() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	sequence := codextui.ClearTerminalProgramStatus()
	if sequence == "" {
		return nil
	}
	return m.programStatusWriter(sequence)
}

// clearTerminalProgramStatusOnExit releases the terminal's lifecycle indicator
// when the TUI exits (Rust #52203/#54114 clear-on-exit behavior).
func clearTerminalProgramStatusOnExit(output io.Writer) {
	if output == nil {
		return
	}
	if sequence := codextui.ClearTerminalProgramStatus(); sequence != "" {
		_, _ = io.WriteString(output, sequence)
	}
}
