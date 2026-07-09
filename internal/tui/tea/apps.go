package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	appsapi "codex_go/internal/apps"
	chatwidget "codex_go/internal/tui/chatwidget"
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
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.AppsLoadingView())
	return func() bubbletea.Msg {
		response, err := reader(threadID, true)
		return AppListResultMsg{ThreadID: threadID, ForceRefetch: true, Response: response, Err: err}
	}
}

func (m *Model) applyAppListResult(message AppListResultMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		m.openSelectionViewModal(ModalKindGeneric, chatwidget.AppsErrorView("Apps: "+strings.TrimSpace(message.Err.Error())))
		return
	}
	m.openAppsView(message.Response)
}

func (m *Model) openAppsView(response appsapi.AppListResponse) {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewAppsCatalogView(response))
	m.notice = "Apps"
}
