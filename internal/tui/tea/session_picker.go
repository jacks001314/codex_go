package tea

import (
	"strings"
	"unicode"
	"unicode/utf8"

	codextui "codex_go/internal/tui"
)

func (m *Model) openSessionPicker(action codextui.SessionPickerAction) {
	picker := codextui.NewSessionPickerState(action, m.sessionItems, m.sessionCWD)
	if picker == nil {
		m.notice = "No sessions available."
		return
	}
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
		if threadID != "" {
			m.State.SetThreadID(threadID)
		}
		return decision, strings.TrimSpace(m.State.RenderSetting("Thread", threadID)), true
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
