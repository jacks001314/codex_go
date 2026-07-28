package tea

import (
	"errors"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/config"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

type hookConfigWriteOperation struct {
	params      config.ConfigBatchWriteParams
	errorPrefix string
}

type hooksBrowserModalState struct {
	view        *bottompane.HooksBrowserView
	eventCursor int
}

func (m *Model) applyHooksCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.onReadHooks == nil {
		m.notice = "Failed to load hooks: hooks/list is unavailable in this runtime"
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return nil
	}
	reader := m.onReadHooks
	cwd := strings.TrimSpace(m.sessionCWD)
	m.notice = "Loading hooks..."
	m.refreshTranscript()
	return func() bubbletea.Msg {
		response, err := reader(cwd)
		return HooksListResultMsg{CWD: cwd, Response: response, Err: err}
	}
}

func (m *Model) applyHooksListResult(message HooksListResultMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		m.notice = "Failed to load hooks: " + strings.TrimSpace(message.Err.Error())
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return
	}
	m.openHooksView(codextui.HooksListEntryForCWD(message.Response, message.CWD))
}

func (m *Model) openHooksView(entry appserver.HookListEntry) {
	if m == nil {
		return
	}
	m.modal = &modalState{
		kind: ModalKindHooksBrowser,
		hooksBrowser: &hooksBrowserModalState{
			view: bottompane.NewHooksBrowserView(entry),
		},
	}
	m.notice = "Hooks"
}

func (m *Model) updateHooksBrowserModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.hooksBrowser == nil || m.modal.hooksBrowser.view == nil {
		return nil
	}
	state := m.modal.hooksBrowser
	for _, key := range manageSkillsKeyNames(message) {
		state.view.HandleKey(key)
	}
	for state.eventCursor < len(state.view.Events) {
		event := state.view.Events[state.eventCursor]
		state.eventCursor++
		switch event.Kind {
		case bottompane.HooksBrowserEventSetEnabled:
			m.hookWriteQueue = append(m.hookWriteQueue, hookConfigWriteOperation{
				params: config.ConfigBatchWriteParams{
					Edits: []config.ConfigEdit{{
						KeyPath: "hooks.state",
						Value: map[string]any{event.Key: map[string]any{
							"enabled": event.Enabled,
						}},
						MergeStrategy: config.MergeUpsert,
					}},
					ReloadUserConfig: true,
				},
				errorPrefix: "Failed to update hook config: ",
			})
		case bottompane.HooksBrowserEventTrustHook:
			m.hookWriteQueue = append(m.hookWriteQueue, hookConfigWriteOperation{
				params:      codextui.BuildSingleHookTrustWriteParams(event.Key, event.CurrentHash),
				errorPrefix: "Failed to trust hook: ",
			})
		case bottompane.HooksBrowserEventTrustHooks:
			m.hookWriteQueue = append(m.hookWriteQueue, hookConfigWriteOperation{
				params:      codextui.BuildHookTrustWriteParams(event.Updates),
				errorPrefix: "Failed to trust hooks: ",
			})
		}
	}
	if state.view.Complete {
		m.modal = nil
		m.refreshTranscript()
	}
	return m.startNextHookConfigWrite()
}

func (m *Model) startNextHookConfigWrite() bubbletea.Cmd {
	if m == nil || m.hookWriteActive || len(m.hookWriteQueue) == 0 {
		return nil
	}
	operation := m.hookWriteQueue[0]
	m.hookWriteQueue = m.hookWriteQueue[1:]
	m.hookWriteActive = true
	m.nextHookWriteRequestID++
	requestID := m.nextHookWriteRequestID
	writer := m.onWriteHookConfig
	return func() bubbletea.Msg {
		var err error
		if writer == nil {
			err = errors.New("config/batchWrite is unavailable")
		} else {
			err = writer(operation.params)
		}
		return HookConfigWriteResultMsg{RequestID: requestID, ErrorPrefix: operation.errorPrefix, Err: err}
	}
}

func (m *Model) applyHookConfigWriteResult(message HookConfigWriteResultMsg) bubbletea.Cmd {
	if m == nil || !m.hookWriteActive || message.RequestID != m.nextHookWriteRequestID {
		return nil
	}
	m.hookWriteActive = false
	if message.Err != nil {
		m.notice = message.ErrorPrefix + message.Err.Error()
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
	}
	return m.startNextHookConfigWrite()
}

func (m *Model) renderHooksBrowserModal() string {
	if m == nil || m.modal == nil || m.modal.hooksBrowser == nil || m.modal.hooksBrowser.view == nil {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	return strings.Join(m.modal.hooksBrowser.view.Rows(width), "\n")
}
