package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/internal/appserver"
	chatwidget "codex_go/internal/tui/chatwidget"
)

func (m *Model) applySkillsManageCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	cwd := strings.TrimSpace(m.sessionCWD)
	if m.onReadSkills == nil {
		m.openSkillsBrowser(appserver.SkillsListResponse{}, cwd)
		return nil
	}
	reader := m.onReadSkills
	m.openSelectionViewModal(ModalKindGeneric, skillsLoadingView())
	return func() bubbletea.Msg {
		response, err := reader(cwd)
		return SkillsListResultMsg{CWD: cwd, Response: response, Err: err}
	}
}

func (m *Model) applySkillsListResult(message SkillsListResultMsg) {
	if m == nil {
		return
	}
	if message.Err == nil {
		response := message.Response
		m.skillsInventory = &response
		m.skillsInventoryCWD = strings.TrimSpace(message.CWD)
		m.skillsInventoryErr = ""
		m.skillsInventoryLoading = false
	}
	if message.Err != nil {
		m.openSelectionViewModal(ModalKindGeneric, skillsErrorView("Skills: "+strings.TrimSpace(message.Err.Error())))
		return
	}
	m.openSkillsBrowser(message.Response, message.CWD)
}

func (m *Model) openSkillsBrowser(response appserver.SkillsListResponse, cwd string) {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.NewSkillsBrowserView(response, cwd))
	m.notice = "Skills"
}

func skillsLoadingView() chatwidget.SelectionView {
	return chatwidget.SelectionView{
		ViewID:      "skills-browser",
		Title:       "Skills",
		Subtitle:    "Loading skills...",
		AllowCancel: true,
		Items: []chatwidget.SelectionItem{{
			Name:     "Loading...",
			Disabled: true,
		}},
	}
}

func skillsErrorView(message string) chatwidget.SelectionView {
	return chatwidget.SelectionView{
		ViewID:      "skills-browser",
		Title:       "Skills",
		Subtitle:    strings.TrimSpace(message),
		AllowCancel: true,
		Items: []chatwidget.SelectionItem{{
			Name:            "Close",
			DismissOnSelect: true,
		}},
	}
}
