package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	appsapi "codex_go/apps"
	chatwidget "codex_go/tui/chatwidget"
)

func (m *Model) applyAppsCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID := ""
	if m.State != nil {
		threadID = strings.TrimSpace(m.State.ThreadID)
	}
	if m.onReadApps == nil {
		m.openAppsView(appsapi.AppListResponse{})
		return nil
	}
	reader := m.onReadApps
	scope := m.appsScopeGeneration
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.AppsLoadingView())
	return func() bubbletea.Msg {
		response, err := reader(threadID, true)
		return AppListResultMsg{ThreadID: threadID, ScopeGeneration: scope, ForceRefetch: true, Response: response, Err: err}
	}
}

func (m *Model) applyAppListResult(message AppListResultMsg) {
	if m == nil {
		return
	}
	// Rust 3c7ae4a812 (#38743): ignore queued app directory results that no
	// longer match the active account/workspace/thread scope, and dismiss any
	// open apps view so stale apps cannot appear in the current context.
	if message.ScopeGeneration != m.appsScopeGeneration {
		if m.appsViewOpen() {
			m.modal = nil
			m.notice = "Apps"
		}
		return
	}
	if message.Err != nil {
		m.openSelectionViewModal(ModalKindGeneric, chatwidget.AppsErrorView("Apps: "+strings.TrimSpace(message.Err.Error())))
		return
	}
	m.openAppsView(message.Response)
}

// invalidateAppsScope revokes cached app directory state and pending requests
// before the active account, workspace, or thread changes (Rust #38743).
func (m *Model) invalidateAppsScope() {
	if m == nil {
		return
	}
	m.appsScopeGeneration++
	if m.appsViewOpen() {
		m.modal = nil
		m.notice = ""
	}
}

func (m *Model) appsViewOpen() bool {
	return m != nil && m.modal != nil && strings.TrimSpace(m.modal.id) == chatwidget.AppsSelectionViewID
}

func (m *Model) openAppsView(response appsapi.AppListResponse) {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewAppsCatalogView(response))
	m.notice = "Apps"
}
