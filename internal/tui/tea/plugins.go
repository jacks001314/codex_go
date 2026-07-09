package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/internal/plugin"
	chatwidget "codex_go/internal/tui/chatwidget"
)

func (m *Model) applyPluginsCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.onReadPlugins == nil {
		m.openPluginsView(plugin.PluginListResponse{})
		return nil
	}
	reader := m.onReadPlugins
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.PluginsLoadingView())
	return func() bubbletea.Msg {
		response, err := reader()
		return PluginListResultMsg{Response: response, Err: err}
	}
}

func (m *Model) applyPluginListResult(message PluginListResultMsg) {
	if m == nil {
		return
	}
	if message.Err != nil {
		m.openSelectionViewModal(ModalKindGeneric, chatwidget.PluginErrorView("Plugins: "+strings.TrimSpace(message.Err.Error()), false))
		return
	}
	m.openPluginsView(message.Response)
}

func (m *Model) openPluginsView(response plugin.PluginListResponse) {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewPluginsCatalogView(response, ""))
	m.notice = "Plugins"
}
