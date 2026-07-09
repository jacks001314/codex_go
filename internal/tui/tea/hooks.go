package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	chatwidget "codex_go/internal/tui/chatwidget"
)

func (m *Model) applyHooksCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.onReadHooks == nil {
		m.openHooksView(m.hookLifecycle.Runs)
		return nil
	}
	reader := m.onReadHooks
	cwd := strings.TrimSpace(m.sessionCWD)
	m.notice = "Loading hooks..."
	m.refreshTranscript()
	return func() bubbletea.Msg {
		runs, err := reader(cwd)
		return HooksListResultMsg{Runs: runs, Err: err}
	}
}

func (m *Model) applyHooksListResult(message HooksListResultMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		m.notice = "Hooks: " + strings.TrimSpace(message.Err.Error())
		m.refreshTranscript()
		return
	}
	m.openHooksView(message.Runs)
}

func (m *Model) openHooksView(runs []chatwidget.HookRun) {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewHooksBrowserView(runs))
	m.notice = "Hooks"
}
