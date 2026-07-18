package tea

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

func (m *Model) applyResumeCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	query := strings.TrimSpace(args)
	if query == "" {
		m.openSessionPicker(codextui.SessionPickerResume)
		return nil
	}
	item, ok, ambiguous := m.findSessionByIDOrName(query)
	if ambiguous {
		m.notice = "Multiple saved chats match '" + query + "'."
		m.openSessionPicker(codextui.SessionPickerResume)
		if m.modal != nil && m.modal.sessionPicker != nil {
			m.modal.sessionPicker.Query = query
			m.modal.sessionPicker.SelectFirst()
		}
		return nil
	}
	if !ok {
		message := "No saved chat found matching '" + query + "'."
		m.notice = message
		m.addHistoryCell(historycell.NewErrorEvent(message))
		m.refreshTranscript()
		return nil
	}
	selection := codextui.SessionSelection{
		Kind: codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{
			Path:     item.Path,
			ThreadID: item.ThreadID,
		},
	}
	decision, notice, _ := m.applySessionSelection(selection)
	if strings.TrimSpace(notice) != "" {
		m.notice = notice
	}
	m.refreshTranscript()
	if decision == nil {
		if strings.TrimSpace(notice) != "" {
			m.addHistoryCell(historycell.NewErrorEvent(strings.TrimSpace(notice)))
			m.refreshTranscript()
		}
		return nil
	}
	if m.onModalResponse != nil {
		return m.onModalResponse(ModalResponse{
			ID:          "session-picker-resume",
			Kind:        ModalKindPicker,
			OptionID:    firstNonEmpty(item.ThreadID, item.Path),
			OptionLabel: item.DisplayTitle(),
			Picker:      decision,
		})
	}
	return nil
}

func (m *Model) findSessionByIDOrName(query string) (codextui.SessionSummary, bool, bool) {
	if m == nil {
		return codextui.SessionSummary{}, false, false
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return codextui.SessionSummary{}, false, false
	}
	lower := strings.ToLower(query)
	var exact []codextui.SessionSummary
	var prefix []codextui.SessionSummary
	for _, item := range m.sessionItems {
		fields := sessionLookupFields(item)
		for _, field := range fields {
			fieldLower := strings.ToLower(strings.TrimSpace(field))
			if fieldLower == "" {
				continue
			}
			if fieldLower == lower {
				exact = append(exact, item)
				break
			}
			if strings.HasPrefix(fieldLower, lower) {
				prefix = append(prefix, item)
				break
			}
		}
	}
	if len(exact) == 1 {
		return exact[0], true, false
	}
	if len(exact) > 1 {
		return codextui.SessionSummary{}, false, true
	}
	if len(prefix) == 1 {
		return prefix[0], true, false
	}
	if len(prefix) > 1 {
		return codextui.SessionSummary{}, false, true
	}
	return codextui.SessionSummary{}, false, false
}

func sessionLookupFields(item codextui.SessionSummary) []string {
	fields := []string{
		item.ThreadID,
		item.Title,
		item.Path,
	}
	if base := filepath.Base(strings.TrimSpace(item.Path)); base != "." && base != "" {
		fields = append(fields, base)
	}
	return fields
}

func (m *Model) openSessionPicker(action codextui.SessionPickerAction) {
	picker := codextui.NewSessionPickerState(action, m.sessionItems, m.sessionCWD)
	if picker == nil {
		m.notice = "No sessions available."
		return
	}
	picker.Density = m.sessionPickerDensity
	visible := picker.VisibleItems()
	if len(visible) == 0 {
		m.notice = "No sessions available to " + action.Label() + "."
		return
	}
	modalOptions := make([]ModalOption, 0, len(visible))
	for i, item := range visible {
		modalOptions = append(modalOptions, ModalOption{
			ID:          firstNonEmpty(item.ThreadID, item.Path),
			Label:       sessionPickerLabel(item),
			Description: sessionPickerDescription(item),
			Shortcut:    modelPickerShortcut(i),
		})
	}
	m.openModal(ModalRequestMsg{
		ID:      "session-picker-" + string(action),
		Kind:    ModalKindPicker,
		Title:   action.Title(),
		Body:    "Choose a session to " + action.Label() + ".",
		Options: modalOptions,
	})
	if m.modal != nil {
		m.modal.sessionPicker = picker
		m.modal.selected = picker.Selected
	}
}

func (m *Model) openSessionActionConfirmation(selection codextui.SessionSelection) {
	action := string(selection.Kind)
	target := strings.TrimSpace(selection.Target.DisplayLabel())
	if target == "" {
		target = "selected session"
	}
	title := "Confirm session " + action
	body := "This will " + action + " " + target + "."
	if selection.Kind == codextui.SessionSelectionDelete {
		body += "\nThis cannot be undone."
	}
	copied := selection
	m.modal = &modalState{
		id:            "session-action-" + action,
		kind:          ModalKindPicker,
		title:         title,
		body:          body,
		sessionAction: &copied,
		options: []ModalOption{
			{ID: "confirm", Label: titleCaseASCII(action), Description: target, Shortcut: "y"},
			{ID: "cancel", Label: "Cancel", Shortcut: "n"},
		},
	}
	m.notice = ""
}

func (m *Model) applySessionSelection(selection codextui.SessionSelection) (*PickerDecision, string, bool) {
	threadID := strings.TrimSpace(selection.Target.ThreadID)
	decision := &PickerDecision{Kind: string(selection.Kind), Value: threadID}
	switch selection.Kind {
	case codextui.SessionSelectionResume:
		if threadID == "" {
			return decision, "Resume failed: missing thread id", true
		}
		if m.onResumeSession != nil {
			response, err := m.onResumeSession(selection)
			if err != nil {
				message := strings.TrimSpace(err.Error())
				if message == "" {
					message = "unknown error"
				}
				return nil, "Resume failed: " + message, true
			}
			if response.Summary != nil {
				m.upsertSessionItem(*response.Summary)
				if strings.TrimSpace(response.Summary.ThreadID) != "" {
					decision.Value = strings.TrimSpace(response.Summary.ThreadID)
				}
			}
			m.applyResumeResponse(threadID, response)
		} else if m.State != nil {
			m.State.SetThreadID(threadID)
			m.State.Messages = nil
			m.setStatus("idle")
			m.refreshTranscript()
			m.transcript.GotoBottom()
		}
		return decision, strings.TrimSpace(m.State.RenderSetting("Thread", firstNonEmpty(decision.Value, threadID))), true
	case codextui.SessionSelectionFork:
		summary, err := m.runSessionAction(selection)
		if err != nil {
			return nil, err.Error(), true
		}
		if summary != nil && strings.TrimSpace(summary.ThreadID) != "" {
			m.upsertSessionItem(*summary)
			m.State.SetThreadID(summary.ThreadID)
			decision.Value = summary.ThreadID
			return decision, "Forked session " + summary.ThreadID, true
		}
		return decision, "Fork selected: " + selection.Target.DisplayLabel(), true
	case codextui.SessionSelectionArchive:
		if _, err := m.runSessionAction(selection); err != nil {
			return nil, err.Error(), true
		}
		m.setSessionArchived(threadID, true)
		return decision, "Archived session " + threadID, true
	case codextui.SessionSelectionUnarchive:
		summary, err := m.runSessionAction(selection)
		if err != nil {
			return nil, err.Error(), true
		}
		if summary != nil {
			summary.Archived = false
			m.upsertSessionItem(*summary)
		} else {
			m.setSessionArchived(threadID, false)
		}
		return decision, "Unarchived session " + threadID, true
	case codextui.SessionSelectionDelete:
		if _, err := m.runSessionAction(selection); err != nil {
			return nil, err.Error(), true
		}
		m.removeSessionItem(threadID)
		if m.State.ThreadID == threadID {
			m.State.ResetThread()
		}
		return decision, "Deleted session " + threadID, true
	default:
		return decision, "", true
	}
}

func (m *Model) applyResumeResponse(threadID string, response SessionResumeResponse) {
	if m == nil || m.State == nil {
		return
	}
	if response.Summary != nil && strings.TrimSpace(response.Summary.ThreadID) != "" {
		threadID = strings.TrimSpace(response.Summary.ThreadID)
	}
	m.State.SetThreadID(threadID)
	m.State.Messages = append([]codextui.Message(nil), response.Messages...)
	status := strings.TrimSpace(response.Status)
	if status == "" {
		status = "idle"
	}
	m.setStatus(status)
	m.activeSide = nil
	m.activeAgentLabel = ""
	if m.statusControls != nil {
		m.statusControls.SetActiveAgentLabel("", false)
	}
	m.refreshTranscript()
	m.transcript.GotoBottom()
}

func normalizeSessionPickerDensityTea(value string) codextui.SessionListDensity {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "comfortable":
		return codextui.SessionDensityComfortable
	default:
		return codextui.SessionDensityDense
	}
}

func (m *Model) runSessionAction(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
	if m == nil || m.onSessionAction == nil {
		return nil, nil
	}
	return m.onSessionAction(selection)
}

func (m *Model) upsertSessionItem(item codextui.SessionSummary) {
	if m == nil || strings.TrimSpace(item.ThreadID) == "" {
		return
	}
	for i := range m.sessionItems {
		if m.sessionItems[i].ThreadID == item.ThreadID {
			m.sessionItems[i] = item
			return
		}
	}
	m.sessionItems = append([]codextui.SessionSummary{item}, m.sessionItems...)
}

func (m *Model) setSessionArchived(threadID string, archived bool) {
	if m == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	for i := range m.sessionItems {
		if m.sessionItems[i].ThreadID == threadID {
			m.sessionItems[i].Archived = archived
			return
		}
	}
}

func (m *Model) removeSessionItem(threadID string) {
	if m == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	items := m.sessionItems[:0]
	for _, item := range m.sessionItems {
		if item.ThreadID != threadID {
			items = append(items, item)
		}
	}
	m.sessionItems = items
}

func sessionPickerLabel(item codextui.SessionSummary) string {
	label := strings.TrimSpace(item.DisplayTitle())
	if label == "" {
		label = firstNonEmpty(item.ThreadID, item.Path, "Untitled session")
	}
	if strings.TrimSpace(item.ThreadID) != "" {
		label += " (" + strings.TrimSpace(item.ThreadID) + ")"
	}
	return label
}

func sessionPickerDescription(item codextui.SessionSummary) string {
	parts := []string{}
	if strings.TrimSpace(item.CWD) != "" {
		parts = append(parts, "cwd: "+strings.TrimSpace(item.CWD))
	}
	if strings.TrimSpace(item.Branch) != "" {
		parts = append(parts, "branch: "+strings.TrimSpace(item.Branch))
	}
	if strings.TrimSpace(item.Provider) != "" {
		parts = append(parts, "provider: "+strings.TrimSpace(item.Provider))
	}
	if item.Archived {
		parts = append(parts, "archived")
	}
	if strings.TrimSpace(item.ThreadID) == "" && strings.TrimSpace(item.Path) == "" {
		parts = append(parts, "updated: "+item.UpdatedAt.Format("2006-01-02"))
	}
	return strings.Join(parts, "  ")
}

func titleCaseASCII(value string) string {
	if value == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(value)
	if r == utf8.RuneError && size == 0 {
		return ""
	}
	return string(unicode.ToUpper(r)) + value[size:]
}
