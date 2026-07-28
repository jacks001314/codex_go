package tea

import (
	"errors"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
)

type skillToggleOperation struct {
	path    string
	enabled bool
}

type manageSkillsModalState struct {
	view        *bottompane.SkillsToggleView
	initial     map[string]bool
	applied     map[string]bool
	eventCursor int
	queue       []skillToggleOperation
	active      *skillToggleOperation
	requestID   uint64
	closing     bool
	cwd         string
}

func (m *Model) openManageSkillsModal(response appserver.SkillsListResponse, cwd string) {
	if m == nil {
		return
	}
	cwd = strings.TrimSpace(cwd)
	skills := chatwidget.SkillsForCWD(cwd, &response)
	if len(response.Data) == 0 && len(skills) == 0 {
		skills = append([]appserver.SkillsListEntry(nil), response.Skills...)
	}
	viewModel := chatwidget.NewManageSkillsView(skills)
	if viewModel.EmptyMessage != "" {
		m.modal = nil
		m.notice = viewModel.EmptyMessage
		m.addInfoHistoryMessage(viewModel.EmptyMessage)
		m.refreshTranscript()
		return
	}

	items := make([]bottompane.SkillsToggleItem, 0, len(viewModel.Items))
	initial := make(map[string]bool, len(viewModel.Items))
	applied := make(map[string]bool, len(viewModel.Items))
	for _, item := range viewModel.Items {
		items = append(items, bottompane.SkillsToggleItem{
			Name:        item.Name,
			SkillName:   item.SkillName,
			Description: item.Description,
			Enabled:     item.Enabled,
			Path:        item.Path,
		})
		initial[item.Path] = item.Enabled
		applied[item.Path] = item.Enabled
	}
	m.modal = &modalState{
		kind: ModalKindManageSkills,
		manageSkills: &manageSkillsModalState{
			view:    bottompane.NewSkillsToggleView(items),
			initial: initial,
			applied: applied,
			cwd:     cwd,
		},
	}
	m.notice = ""
}

func (m *Model) updateManageSkillsModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.manageSkills == nil {
		return nil
	}
	state := m.modal.manageSkills
	if state.closing {
		return nil
	}
	for _, key := range manageSkillsKeyNames(message) {
		state.view.HandleKey(key)
	}
	for state.eventCursor < len(state.view.Events) {
		event := state.view.Events[state.eventCursor]
		state.eventCursor++
		switch event.Kind {
		case bottompane.SkillsToggleEventSetEnabled:
			state.queue = append(state.queue, skillToggleOperation{path: event.Path, enabled: event.Enabled})
		case bottompane.SkillsToggleEventClosed:
			state.closing = true
		case bottompane.SkillsToggleEventReload:
			// Reload happens after all queued writes, preserving Rust's event order.
		}
	}
	return m.startNextSkillWrite()
}

func manageSkillsKeyNames(message bubbletea.KeyMsg) []string {
	switch message.Type {
	case bubbletea.KeyRunes:
		keys := make([]string, 0, len(message.Runes))
		for _, value := range message.Runes {
			if value == ' ' {
				keys = append(keys, "space")
			} else {
				keys = append(keys, string(value))
			}
		}
		return keys
	case bubbletea.KeyUp:
		return []string{"up"}
	case bubbletea.KeyCtrlP:
		return []string{"ctrl+p"}
	case bubbletea.KeyDown:
		return []string{"down"}
	case bubbletea.KeyCtrlN:
		return []string{"ctrl+n"}
	case bubbletea.KeyPgUp:
		return []string{"pageup"}
	case bubbletea.KeyCtrlU:
		return []string{"ctrl+u"}
	case bubbletea.KeyPgDown:
		return []string{"pagedown"}
	case bubbletea.KeyCtrlD:
		return []string{"ctrl+d"}
	case bubbletea.KeyHome, bubbletea.KeyCtrlHome:
		return []string{"home"}
	case bubbletea.KeyCtrlA:
		return []string{"ctrl+a"}
	case bubbletea.KeyEnd, bubbletea.KeyCtrlEnd:
		return []string{"end"}
	case bubbletea.KeyCtrlE:
		return []string{"ctrl+e"}
	case bubbletea.KeyBackspace:
		return []string{"backspace"}
	case bubbletea.KeySpace:
		return []string{"space"}
	case bubbletea.KeyEnter:
		return []string{"enter"}
	case bubbletea.KeyEsc:
		return []string{"esc"}
	case bubbletea.KeyCtrlC:
		return []string{"ctrl+c"}
	default:
		return nil
	}
}

func (m *Model) startNextSkillWrite() bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.manageSkills == nil {
		return nil
	}
	state := m.modal.manageSkills
	if state.active != nil {
		return nil
	}
	if len(state.queue) == 0 {
		if state.closing {
			return m.finishManageSkillsModal(state)
		}
		return nil
	}
	operation := state.queue[0]
	state.queue = state.queue[1:]
	state.active = &operation
	m.nextSkillWriteRequestID++
	state.requestID = m.nextSkillWriteRequestID
	requestID := state.requestID
	writer := m.onWriteSkillEnabled
	return func() bubbletea.Msg {
		if writer == nil {
			return SkillEnabledWriteResultMsg{
				RequestID: requestID,
				Path:      operation.path,
				Enabled:   operation.enabled,
				Err:       errors.New("skills/config/write is unavailable"),
			}
		}
		_, err := writer(operation.path, operation.enabled)
		return SkillEnabledWriteResultMsg{
			RequestID: requestID,
			Path:      operation.path,
			Enabled:   operation.enabled,
			Err:       err,
		}
	}
}

func (m *Model) applySkillEnabledWriteResult(message SkillEnabledWriteResultMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.manageSkills == nil {
		return nil
	}
	state := m.modal.manageSkills
	if state.active == nil || state.requestID != message.RequestID {
		return nil
	}
	state.active = nil
	if message.Err != nil {
		text := "Failed to update skill config for " + message.Path + ": " + message.Err.Error()
		m.notice = text
		m.addErrorHistoryMessage(text)
		m.refreshTranscript()
	} else {
		state.applied[message.Path] = message.Enabled
		m.updateSkillInventoryEnabled(message.Path, message.Enabled)
	}
	return m.startNextSkillWrite()
}

func (m *Model) updateSkillInventoryEnabled(path string, enabled bool) {
	if m == nil || m.skillsInventory == nil {
		return
	}
	for index := range m.skillsInventory.Skills {
		if strings.TrimSpace(m.skillsInventory.Skills[index].Path) == strings.TrimSpace(path) {
			m.skillsInventory.Skills[index].Enabled = enabled
		}
	}
	for dataIndex := range m.skillsInventory.Data {
		for skillIndex := range m.skillsInventory.Data[dataIndex].Skills {
			if strings.TrimSpace(m.skillsInventory.Data[dataIndex].Skills[skillIndex].Path) == strings.TrimSpace(path) {
				m.skillsInventory.Data[dataIndex].Skills[skillIndex].Enabled = enabled
			}
		}
	}
	if m.mentionPopup != nil {
		m.mentionPopup.SetCandidates(m.mentionCandidates())
	}
}

func (m *Model) finishManageSkillsModal(state *manageSkillsModalState) bubbletea.Cmd {
	if m == nil || state == nil {
		return nil
	}
	_, _, summary, changed := chatwidget.ManageSkillsChangeSummary(state.initial, state.applied)
	m.modal = nil
	if changed {
		m.notice = summary
		m.addInfoHistoryMessage(summary)
	}
	m.refreshTranscript()
	if m.onReadSkills == nil {
		return nil
	}
	m.skillsInventoryLoading = true
	reader := m.onReadSkills
	cwd := state.cwd
	return func() bubbletea.Msg {
		response, err := reader(cwd, true)
		return SkillsInventoryResultMsg{CWD: cwd, Response: response, Err: err}
	}
}

func (m *Model) renderManageSkillsModal() string {
	if m == nil || m.modal == nil || m.modal.manageSkills == nil || m.modal.manageSkills.view == nil {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	return strings.Join(m.modal.manageSkills.view.Rows(width), "\n")
}
