package tea

import (
	"fmt"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

type ModalKind string

const (
	ModalKindApproval        ModalKind = "approval"
	ModalKindElicitation     ModalKind = "elicitation"
	ModalKindPicker          ModalKind = "picker"
	ModalKindUserInput       ModalKind = "user_input"
	ModalKindUsage           ModalKind = "usage"
	ModalKindStatusLine      ModalKind = "status_line"
	ModalKindTitle           ModalKind = "terminal_title"
	ModalKindPermissions     ModalKind = "permissions"
	ModalKindPersonality     ModalKind = "personality"
	ModalKindExperimental    ModalKind = "experimental"
	ModalKindRateLimitSwitch ModalKind = "rate_limit_switch"
	ModalKindReview          ModalKind = "review"
	ModalKindSkills          ModalKind = "skills"
	ModalKindAgent           ModalKind = "agent"
	ModalKindTheme           ModalKind = "theme"
	ModalKindPets            ModalKind = "pets"
	ModalKindGeneric         ModalKind = "generic"
)

type ModalOption struct {
	ID          string
	Label       string
	Description string
	Shortcut    string
	Disabled    bool
}

type ModalRequestMsg struct {
	ID      string
	Kind    ModalKind
	Title   string
	Body    string
	Options []ModalOption
}

type ApprovalRequestMsg struct {
	ID      string
	Title   string
	Body    string
	Command string
	Options []ModalOption
}

type ModalResponse struct {
	ID          string
	Kind        ModalKind
	OptionID    string
	OptionLabel string
	Cancelled   bool
	Elicitation *ElicitationDecision
	Picker      *PickerDecision
	UserInput   *UserInputDecision
}

type ModalResponseFunc func(ModalResponse) bubbletea.Cmd

type ElicitationDecision struct {
	Action  string
	Content map[string]any
	Persist string
}

type PickerDecision struct {
	Kind            string
	Value           string
	ReasoningEffort string
	Scope           string
}

type UserInputDecision struct {
	Answers     map[string]string
	AnswerLists map[string][]string
	TimedOut    bool
}

type modalState struct {
	id       string
	kind     ModalKind
	title    string
	body     string
	options  []ModalOption
	selected int

	elicitation        *bottompane.ElicitationFormRequest
	modelPicker        *codextui.ModelPicker
	modelReasoning     *codextui.ModelReasoningPicker
	planReasoningScope *codextui.PlanReasoningScopePicker
	sessionPicker      *codextui.SessionPickerState
	sessionAction      *codextui.SessionSelection
	themePicker        *codextui.ThemePicker
	themeFilter        string
	themeSubtitle      string
	userInput          *codextui.RequestUserInputState
	statusLineSetup    *statusLineSetupModal
	terminalTitleSetup *terminalTitleSetupModal
}

func DefaultApprovalOptions() []ModalOption {
	return []ModalOption{
		{ID: "allow_once", Label: "Allow for this turn", Shortcut: "y"},
		{ID: "allow_session", Label: "Allow for this session", Shortcut: "a"},
		{ID: "deny", Label: "Deny", Shortcut: "d"},
	}
}

func (m *Model) openApprovalModal(message ApprovalRequestMsg) bubbletea.Cmd {
	body := strings.TrimSpace(message.Body)
	if strings.TrimSpace(message.Command) != "" {
		if body != "" {
			body += "\n"
		}
		body += strings.TrimSpace(message.Command)
	}
	options := message.Options
	if len(options) == 0 {
		options = DefaultApprovalOptions()
	}
	m.openModal(ModalRequestMsg{
		ID:      message.ID,
		Kind:    ModalKindApproval,
		Title:   firstNonEmpty(message.Title, "Approval request"),
		Body:    body,
		Options: options,
	})
	return m.queueNotification(chatwidget.ExecApprovalRequestedNotification(message.Command))
}

func (m *Model) openModal(message ModalRequestMsg) {
	options := normalizeModalOptions(message.Options)
	if len(options) == 0 {
		options = []ModalOption{{ID: "ok", Label: "OK", Shortcut: "enter"}}
	}
	m.modal = &modalState{
		id:      strings.TrimSpace(message.ID),
		kind:    message.Kind,
		title:   firstNonEmpty(message.Title, "Select an option"),
		body:    strings.TrimSpace(message.Body),
		options: options,
	}
	m.notice = ""
}

func normalizeModalOptions(options []ModalOption) []ModalOption {
	out := make([]ModalOption, 0, len(options))
	for i, option := range options {
		label := strings.TrimSpace(option.Label)
		if label == "" {
			continue
		}
		id := strings.TrimSpace(option.ID)
		if id == "" {
			id = fmt.Sprintf("option_%d", i+1)
		}
		out = append(out, ModalOption{
			ID:          id,
			Label:       label,
			Description: strings.TrimSpace(option.Description),
			Shortcut:    strings.ToLower(strings.TrimSpace(option.Shortcut)),
			Disabled:    option.Disabled,
		})
	}
	return out
}

func (m *Model) updateModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil {
		return nil
	}
	if m.modal.themePicker != nil {
		return m.updateThemePickerModal(message)
	}
	if m.modal.sessionPicker != nil {
		return m.updateSessionPickerModal(message)
	}
	if m.modal.userInput != nil {
		userInput := m.modal.userInput
		if userInput.ConfirmUnanswered && message.Type == bubbletea.KeyEsc {
			return m.respondModal(true)
		}
		if !userInput.ConfirmUnanswered && userInput.HasOptions() {
			switch message.Type {
			case bubbletea.KeyTab:
				if userInput.NotesVisible {
					userInput.ClearNotes()
				} else {
					userInput.BeginNotes()
				}
				m.refreshRequestUserInputModal()
				return nil
			case bubbletea.KeyEsc:
				if userInput.NotesVisible {
					userInput.ClearNotes()
					m.refreshRequestUserInputModal()
					return nil
				}
			case bubbletea.KeyBackspace:
				if userInput.NotesVisible {
					userInput.BackspaceDraft()
					if userInput.Draft == "" {
						userInput.ClearNotes()
					}
					m.refreshRequestUserInputModal()
					return nil
				}
			case bubbletea.KeyRunes:
				key := strings.ToLower(string(message.Runes))
				if userInput.NotesVisible {
					userInput.AppendDraft(string(message.Runes))
					m.refreshRequestUserInputModal()
					return nil
				}
				if !m.requestUserInputShortcutMatches(key) {
					userInput.BeginNotes()
					userInput.AppendDraft(string(message.Runes))
					m.refreshRequestUserInputModal()
					return nil
				}
			}
		}
		if !userInput.ConfirmUnanswered && !userInput.HasOptions() {
			switch message.Type {
			case bubbletea.KeyRunes:
				userInput.AppendDraft(string(message.Runes))
				m.refreshRequestUserInputModal()
				return nil
			case bubbletea.KeyBackspace:
				userInput.BackspaceDraft()
				m.refreshRequestUserInputModal()
				return nil
			}
		}
	}
	if m.modal.statusLineSetup != nil || m.modal.terminalTitleSetup != nil {
		switch message.Type {
		case bubbletea.KeySpace:
			return m.toggleStatusSetupSelection()
		case bubbletea.KeyRunes:
			if string(message.Runes) == " " {
				return m.toggleStatusSetupSelection()
			}
		}
	}
	if m.modal.kind == ModalKindExperimental {
		switch message.Type {
		case bubbletea.KeySpace:
			m.toggleExperimentalSelection()
			return nil
		case bubbletea.KeyRunes:
			if string(message.Runes) == " " {
				m.toggleExperimentalSelection()
				return nil
			}
		}
	}
	switch message.Type {
	case bubbletea.KeyEsc:
		return m.respondModal(true)
	case bubbletea.KeyEnter:
		if m.modal.options[m.modal.selected].Disabled {
			return nil
		}
		return m.respondModal(false)
	case bubbletea.KeyUp:
		m.moveModalSelection(-1)
		return nil
	case bubbletea.KeyDown, bubbletea.KeyTab:
		m.moveModalSelection(1)
		return nil
	case bubbletea.KeyRunes:
		key := strings.ToLower(string(message.Runes))
		if key == "" {
			return nil
		}
		for i, option := range m.modal.options {
			if option.Disabled {
				continue
			}
			if option.Shortcut == key || fmt.Sprintf("%d", i+1) == key {
				m.modal.selected = i
				return m.respondModal(false)
			}
		}
	}
	return nil
}

func (m *Model) updateSessionPickerModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.sessionPicker == nil {
		return nil
	}
	picker := m.modal.sessionPicker
	switch message.Type {
	case bubbletea.KeyEsc:
		if strings.TrimSpace(picker.Query) != "" {
			picker.Query = ""
			picker.SelectFirst()
			m.syncSessionPickerModalSelection()
			return nil
		}
		return m.respondModal(true)
	case bubbletea.KeyCtrlC:
		return m.respondModal(true)
	case bubbletea.KeyEnter:
		return m.respondModal(false)
	case bubbletea.KeyUp:
		picker.Move(-1)
	case bubbletea.KeyDown:
		picker.Move(1)
	case bubbletea.KeyPgUp:
		picker.MovePage(-sessionPickerPageStep(m))
	case bubbletea.KeyPgDown:
		picker.MovePage(sessionPickerPageStep(m))
	case bubbletea.KeyHome, bubbletea.KeyCtrlHome:
		picker.SelectFirst()
	case bubbletea.KeyEnd, bubbletea.KeyCtrlEnd:
		picker.SelectLast()
	case bubbletea.KeyTab, bubbletea.KeyShiftTab:
		picker.ToggleToolbarFocus()
	case bubbletea.KeyLeft, bubbletea.KeyRight:
		picker.ChangeFocusedToolbarValue()
	case bubbletea.KeyCtrlO:
		picker.ToggleDensity()
	case bubbletea.KeyCtrlE:
		if item, ok := picker.SelectedItem(); ok {
			picker.ToggleExpanded(item.ThreadID)
		}
	case bubbletea.KeyBackspace:
		if picker.Query != "" {
			runes := []rune(picker.Query)
			picker.Query = string(runes[:len(runes)-1])
			picker.SelectFirst()
		}
	case bubbletea.KeyRunes:
		text := string(message.Runes)
		if text == "" {
			return nil
		}
		if picker.Action != codextui.SessionPickerResume && strings.TrimSpace(picker.Query) == "" {
			for i := range picker.VisibleItems() {
				if modelPickerShortcut(i) == text {
					picker.Select(i)
					m.syncSessionPickerModalSelection()
					return m.respondModal(false)
				}
				if i >= 8 {
					break
				}
			}
		}
		lower := strings.ToLower(text)
		switch lower {
		case "o":
			if message.Alt {
				return nil
			}
		case "e":
			if message.Alt {
				return nil
			}
		}
		if text == " " || !message.Alt {
			picker.Query += text
			picker.SelectFirst()
		}
	}
	m.syncSessionPickerModalSelection()
	return nil
}

func sessionPickerPageStep(m *Model) int {
	if m == nil || m.height <= 0 {
		return 10
	}
	step := sessionPickerListHeight(m.height)
	if step < 1 {
		return 1
	}
	return step
}

func (m *Model) syncSessionPickerModalSelection() {
	if m == nil || m.modal == nil || m.modal.sessionPicker == nil {
		return
	}
	m.modal.sessionPicker.Select(m.modal.sessionPicker.Selected)
	m.modal.selected = m.modal.sessionPicker.Selected
}

func (m *Model) requestUserInputShortcutMatches(key string) bool {
	if m == nil || m.modal == nil || strings.TrimSpace(key) == "" {
		return false
	}
	for i, option := range m.modal.options {
		if option.Disabled {
			continue
		}
		if option.Shortcut == key || fmt.Sprintf("%d", i+1) == key {
			return true
		}
	}
	return false
}

func (m *Model) moveModalSelection(delta int) {
	if m == nil || m.modal == nil || len(m.modal.options) == 0 {
		return
	}
	next := m.modal.selected
	for range m.modal.options {
		next = (next + delta) % len(m.modal.options)
		if next < 0 {
			next += len(m.modal.options)
		}
		if !m.modal.options[next].Disabled {
			m.modal.selected = next
			break
		}
	}
	if m.modal.modelPicker != nil {
		m.modal.modelPicker.Select(m.modal.selected)
	}
	if m.modal.modelReasoning != nil {
		m.modal.modelReasoning.Select(m.modal.selected)
	}
	if m.modal.planReasoningScope != nil {
		m.modal.planReasoningScope.Select(m.modal.selected)
	}
	if m.modal.sessionPicker != nil {
		m.modal.sessionPicker.Select(m.modal.selected)
	}
}

func (m *Model) respondModal(cancelled bool) bubbletea.Cmd {
	if m == nil || m.modal == nil {
		return nil
	}
	modal := m.modal
	response := ModalResponse{
		ID:        modal.id,
		Kind:      modal.kind,
		Cancelled: cancelled,
	}
	if !cancelled && modal.sessionPicker != nil {
		if item, ok := modal.sessionPicker.SelectedItem(); ok {
			response.OptionID = firstNonEmpty(item.ThreadID, item.Path)
			response.OptionLabel = item.DisplayTitle()
		}
	} else if !cancelled && len(modal.options) > 0 {
		if modal.options[modal.selected].Disabled {
			return nil
		}
		response.OptionID = modal.options[modal.selected].ID
		response.OptionLabel = modal.options[modal.selected].Label
	} else if cancelled {
		m.notice = "Cancelled"
	}
	if modal.kind == ModalKindElicitation {
		decision, ok := m.resolveElicitationModal(modal, response.OptionID, cancelled)
		if !ok {
			return nil
		}
		response.Elicitation = decision
	}
	notice := ""
	if modal.kind == ModalKindPicker && (modal.modelPicker != nil || modal.modelReasoning != nil || modal.planReasoningScope != nil || modal.sessionPicker != nil || modal.sessionAction != nil) {
		decision, pickerNotice, complete := m.resolveModelPickerModal(modal, response.OptionID, cancelled)
		if !complete {
			return nil
		}
		response.Picker = decision
		notice = pickerNotice
	}
	if modal.kind == ModalKindUserInput && modal.userInput != nil {
		decision, complete, ok := m.resolveRequestUserInputModal(modal, response.OptionID, cancelled)
		if !ok {
			return nil
		}
		if !complete {
			return nil
		}
		response.UserInput = decision
	}
	if modal.kind == ModalKindUsage {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyUsageModalOption(response.OptionID)
	}
	if modal.kind == ModalKindPermissions {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyPermissionsModalOption(response.OptionID)
	}
	if modal.kind == ModalKindPersonality {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyPersonalityModalOption(response.OptionID)
	}
	if modal.kind == ModalKindExperimental {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyExperimentalModalOption(response.OptionID)
	}
	if modal.kind == ModalKindRateLimitSwitch {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyRateLimitSwitchModalOption(response.OptionID)
	}
	if modal.kind == ModalKindReview {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyReviewModalOption(response.OptionID, response.OptionLabel)
	}
	if modal.kind == ModalKindSkills {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applySkillsModalOption(response.OptionID)
	}
	if modal.kind == ModalKindAgent {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyAgentModalOption(response.OptionID)
	}
	if modal.kind == ModalKindTheme {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyThemeModalOption(response.OptionID)
	}
	if modal.kind == ModalKindPets {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyPetsModalOption(response.OptionID)
	}
	if modal.kind == ModalKindStatusLine && modal.statusLineSetup != nil {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.commitStatusLineSetup(modal.statusLineSetup)
	}
	if modal.kind == ModalKindTitle && modal.terminalTitleSetup != nil {
		m.modal = nil
		if cancelled {
			return m.cancelTerminalTitleSetup()
		}
		return m.commitTerminalTitleSetup(modal.terminalTitleSetup)
	}
	m.modal = nil
	if !cancelled && len(modal.options) > 0 {
		if notice != "" {
			m.notice = notice
		} else {
			m.notice = modal.options[modal.selected].Label
		}
	}
	if m.onModalResponse != nil {
		return m.onModalResponse(response)
	}
	return nil
}

func (m *Model) resolveRequestUserInputModal(modal *modalState, optionID string, cancelled bool) (*UserInputDecision, bool, bool) {
	if modal == nil || modal.userInput == nil {
		return &UserInputDecision{Answers: map[string]string{}, AnswerLists: map[string][]string{}}, true, true
	}
	if modal.userInput.ConfirmUnanswered {
		if cancelled || optionID == "go_back" {
			modal.userInput.CloseUnansweredConfirmation()
			modal.userInput.JumpToFirstUnanswered()
			m.notice = ""
			m.refreshRequestUserInputModal()
			return nil, false, false
		}
		if optionID == "proceed" {
			return &UserInputDecision{
				Answers:     modal.userInput.ResponseAnswers(),
				AnswerLists: modal.userInput.ResponseAnswerLists(),
			}, true, true
		}
	}
	if cancelled {
		return &UserInputDecision{Answers: map[string]string{}, AnswerLists: map[string][]string{}}, true, true
	}
	question, ok := modal.userInput.CurrentQuestion()
	if !ok {
		return &UserInputDecision{Answers: modal.userInput.ResponseAnswers(), AnswerLists: modal.userInput.ResponseAnswerLists()}, true, true
	}
	var complete bool
	if len(question.Options) > 0 {
		index := modal.selected
		if strings.HasPrefix(optionID, "option_") {
			if _, err := fmt.Sscanf(optionID, "option_%d", &index); err != nil {
				index = modal.selected
			}
		}
		maxOptions := len(question.Options)
		otherSelected := question.IsOther && index == maxOptions
		if index < 0 || index >= maxOptions && !otherSelected {
			m.notice = "Select an answer."
			return nil, false, false
		}
		label := codextui.RequestUserInputOtherOptionLabel
		if !otherSelected {
			label = question.Options[index].Label
		}
		complete = modal.userInput.CommitOptionAnswer(label, modal.userInput.Draft)
	} else {
		complete = modal.userInput.CommitFreeformAnswer(modal.userInput.Draft)
	}
	if complete && modal.userInput.UnansweredCount() > 0 {
		modal.userInput.OpenUnansweredConfirmation()
		m.refreshRequestUserInputModal()
		return nil, false, false
	}
	if !complete {
		m.refreshRequestUserInputModal()
		return nil, false, false
	}
	return &UserInputDecision{
		Answers:     modal.userInput.ResponseAnswers(),
		AnswerLists: modal.userInput.ResponseAnswerLists(),
	}, true, true
}

func (m *Model) resolveModelPickerModal(modal *modalState, optionID string, cancelled bool) (*PickerDecision, string, bool) {
	if cancelled || modal == nil {
		return nil, "", true
	}
	if modal.sessionAction != nil {
		selection := *modal.sessionAction
		if optionID != "confirm" {
			return nil, "Cancelled", true
		}
		decision, notice, ok := m.applySessionSelection(selection)
		return decision, notice, ok
	}
	if modal.sessionPicker != nil {
		selection, ok := modal.sessionPicker.Selection()
		if !ok {
			return nil, "", true
		}
		switch selection.Kind {
		case codextui.SessionSelectionArchive, codextui.SessionSelectionDelete:
			m.openSessionActionConfirmation(selection)
			return nil, "", false
		default:
			decision, notice, complete := m.applySessionSelection(selection)
			return decision, notice, complete
		}
	}
	if modal.planReasoningScope != nil {
		scopeOption, ok := modal.planReasoningScope.OptionByID(optionID)
		if !ok {
			return nil, "", true
		}
		modelID := modal.planReasoningScope.Model.ID
		effort := modal.planReasoningScope.Effort
		m.State.Model = modelID
		notice := strings.TrimSpace(m.State.RenderSetting("Model", m.State.Model))
		switch scopeOption.Scope {
		case codextui.PlanReasoningScopeAllModes:
			m.State.ReasoningEffort = effort
			m.State.PlanModeReasoningEffort = effort
			notice += "\n" + strings.TrimSpace(m.State.RenderSetting("Reasoning", m.State.ReasoningEffort))
			notice += "\n" + strings.TrimSpace(m.State.RenderSetting("Plan Reasoning", m.State.PlanModeReasoningEffort))
		default:
			m.State.PlanModeReasoningEffort = effort
			notice += "\n" + strings.TrimSpace(m.State.RenderSetting("Plan Reasoning", m.State.PlanModeReasoningEffort))
		}
		return &PickerDecision{
			Kind:            "plan_reasoning_scope",
			Value:           modelID,
			ReasoningEffort: effort,
			Scope:           string(scopeOption.Scope),
		}, notice, true
	}
	if modal.modelReasoning != nil {
		effort, ok := modal.modelReasoning.EffortByID(optionID)
		if !ok {
			return nil, "", true
		}
		if m.State.ShouldPromptPlanReasoningScope(modal.modelReasoning.Model.ID, effort.Effort) {
			m.openPlanReasoningScopePicker(modal.modelReasoning.Model, effort.Effort)
			return nil, "", false
		}
		m.State.Model = modal.modelReasoning.Model.ID
		m.State.ReasoningEffort = effort.Effort
		notice := strings.TrimSpace(m.State.RenderSetting("Model", m.State.Model)) + "\n" +
			strings.TrimSpace(m.State.RenderSetting("Reasoning", m.State.ReasoningEffort))
		return &PickerDecision{Kind: "model_reasoning", Value: m.State.Model, ReasoningEffort: m.State.ReasoningEffort}, notice, true
	}
	if modal.modelPicker == nil {
		return nil, "", true
	}
	option, ok := modal.modelPicker.OptionByID(optionID)
	if !ok {
		return nil, "", true
	}
	if option.NeedsReasoningPicker() {
		m.openModelReasoningPicker(option)
		return nil, "", false
	}
	reasoning := option.DefaultReasoning()
	if m.State.ShouldPromptPlanReasoningScope(option.ID, reasoning) {
		m.openPlanReasoningScopePicker(option, reasoning)
		return nil, "", false
	}
	m.State.Model = option.ID
	if reasoning != "" {
		m.State.ReasoningEffort = reasoning
	}
	return &PickerDecision{Kind: "model", Value: option.ID, ReasoningEffort: m.State.ReasoningEffort}, strings.TrimSpace(m.State.RenderSetting("Model", m.State.Model)), true
}

func (m *Model) resolveElicitationModal(modal *modalState, optionID string, cancelled bool) (*ElicitationDecision, bool) {
	if cancelled {
		return &ElicitationDecision{Action: string(bottompane.ElicitationCancel)}, true
	}
	if modal == nil || modal.elicitation == nil {
		switch optionID {
		case "accept":
			return &ElicitationDecision{Action: string(bottompane.ElicitationAccept)}, true
		case "decline":
			return &ElicitationDecision{Action: string(bottompane.ElicitationDecline)}, true
		default:
			return &ElicitationDecision{Action: string(bottompane.ElicitationCancel)}, true
		}
	}
	request := modal.elicitation
	if request.ResponseMode == bottompane.ElicitationApprovalAction {
		if len(request.Fields) > 0 {
			request.SetValue(request.Fields[0].Name, optionID)
		}
		decision, err := request.Submit()
		if err != nil {
			m.notice = err.Error()
			return nil, false
		}
		return elicitationDecisionFromBottomPane(decision), true
	}
	switch optionID {
	case "accept":
		decision, err := request.Submit()
		if err != nil {
			m.notice = err.Error()
			return nil, false
		}
		return elicitationDecisionFromBottomPane(decision), true
	case "decline":
		return &ElicitationDecision{Action: string(bottompane.ElicitationDecline)}, true
	default:
		return &ElicitationDecision{Action: string(bottompane.ElicitationCancel)}, true
	}
}

func elicitationDecisionFromBottomPane(decision bottompane.ElicitationDecision) *ElicitationDecision {
	return &ElicitationDecision{
		Action:  string(decision.Action),
		Content: cloneAnyMap(decision.Content),
		Persist: decision.Persist,
	}
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (m *Model) updateThemePickerModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.themePicker == nil {
		return nil
	}
	modal := m.modal
	picker := modal.themePicker
	switch message.Type {
	case bubbletea.KeyEsc:
		picker.Cancel()
		m.modal = nil
		m.notice = "Cancelled"
		return nil
	case bubbletea.KeyEnter:
		if !themePickerSelectionVisible(modal) {
			return nil
		}
		themeID := picker.Confirm()
		m.modal = nil
		if strings.TrimSpace(themeID) == "" {
			m.notice = "Unknown theme"
			m.refreshTranscript()
			return nil
		}
		return m.setTUITheme(themeID)
	case bubbletea.KeyUp:
		picker.MoveFiltered(-1, modal.themeFilter)
		return nil
	case bubbletea.KeyDown, bubbletea.KeyTab:
		picker.MoveFiltered(1, modal.themeFilter)
		return nil
	case bubbletea.KeyBackspace:
		if modal.themeFilter != "" {
			runes := []rune(modal.themeFilter)
			modal.themeFilter = string(runes[:len(runes)-1])
			ensureThemePickerSelection(modal)
		}
		return nil
	case bubbletea.KeyRunes:
		text := string(message.Runes)
		if text == "" {
			return nil
		}
		modal.themeFilter += text
		ensureThemePickerSelection(modal)
		return nil
	}
	return nil
}

func ensureThemePickerSelection(modal *modalState) {
	if modal == nil || modal.themePicker == nil {
		return
	}
	indices := modal.themePicker.FilteredIndices(modal.themeFilter)
	if len(indices) == 0 {
		return
	}
	for _, index := range indices {
		if index == modal.themePicker.Selected {
			return
		}
	}
	modal.themePicker.Select(indices[0])
}

func themePickerSelectionVisible(modal *modalState) bool {
	if modal == nil || modal.themePicker == nil {
		return false
	}
	indices := modal.themePicker.FilteredIndices(modal.themeFilter)
	for _, index := range indices {
		if index == modal.themePicker.Selected {
			return true
		}
	}
	return false
}

func (m *Model) renderModal() string {
	if m == nil || m.modal == nil {
		return ""
	}
	if m.modal.themePicker != nil {
		return m.renderThemePickerModal()
	}
	if m.modal.sessionPicker != nil {
		return m.renderSessionPickerModal()
	}
	var builder strings.Builder
	builder.WriteString(m.modal.title)
	builder.WriteString("\n")
	if m.modal.body != "" {
		builder.WriteString(indentLines(m.modal.body, "  "))
		builder.WriteString("\n")
	}
	for i, option := range m.modal.options {
		selected := i == m.modal.selected
		prefix := codextui.NumberedSelectionPrefix(i, selected)
		check := ""
		if m.modal.statusLineSetup != nil || m.modal.terminalTitleSetup != nil {
			check = "[ ] "
			if setupOptionSelected(m.modal, option.ID) {
				check = "[x] "
			}
		}
		line := prefix + check + option.Label
		if option.Disabled {
			line += " [disabled]"
		}
		if option.Shortcut != "" && option.Shortcut != "enter" {
			line += " (" + option.Shortcut + ")"
		}
		if option.Description != "" {
			line += " - " + option.Description
		}
		if selected {
			line = codextui.RenderSelectedRow(line)
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	footer := "Esc cancel | Enter choose"
	if m.modal.statusLineSetup != nil || m.modal.terminalTitleSetup != nil {
		footer = "Space toggle | Enter save | Esc cancel"
	} else if m.modal.kind == ModalKindExperimental {
		footer = "Space toggle | Enter save | Esc cancel"
	}
	if m.modal.userInput != nil {
		switch {
		case m.modal.userInput.ConfirmUnanswered:
			footer = "Press enter to confirm or esc to go back"
		case m.modal.userInput.NotesVisible:
			footer = "Tab or Esc clear notes | Enter submit answer"
		}
	}
	builder.WriteString(footer)
	return strings.TrimRight(builder.String(), "\n")
}

func (m *Model) renderSessionPickerModal() string {
	if m == nil || m.modal == nil || m.modal.sessionPicker == nil {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	contentWidth := width
	if contentWidth > 2 {
		contentWidth -= 2
	}
	picker := m.modal.sessionPicker
	var builder strings.Builder
	builder.WriteString(m.modal.title)
	builder.WriteString("\n\n")
	builder.WriteString(picker.SearchLine(contentWidth))
	builder.WriteString("\n\n")
	rows := picker.RenderRows(contentWidth, m.currentTime())
	maxRows := sessionPickerListHeight(m.height)
	if maxRows > 0 && len(rows) > maxRows {
		rows = rows[:maxRows]
	}
	for _, row := range rows {
		builder.WriteString(row)
		builder.WriteString("\n")
	}
	if len(rows) > 0 {
		builder.WriteString("\n")
	}
	for _, row := range picker.FooterLines(contentWidth, true) {
		builder.WriteString(row)
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

func sessionPickerListHeight(height int) int {
	if height <= 0 {
		return 10
	}
	value := height - 10
	if value < 3 {
		return 3
	}
	return value
}

func (m *Model) renderThemePickerModal() string {
	if m == nil || m.modal == nil || m.modal.themePicker == nil {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	contentWidth := width
	if contentWidth > 2 {
		contentWidth -= 2
	}
	layout := codextui.ComputeThemePreviewLayout(contentWidth)
	var builder strings.Builder
	builder.WriteString(m.modal.title)
	builder.WriteString("\n")
	subtitle := strings.TrimSpace(m.modal.themeSubtitle)
	if subtitle != "" {
		builder.WriteString(renderThemePickerDimLine(subtitle, contentWidth))
		builder.WriteString("\n")
	}
	builder.WriteString("\n")

	listWidth := layout.ListWidth
	if listWidth <= 0 {
		listWidth = contentWidth
	}
	prompt := renderThemePickerSearchPrompt(m.modal.themeFilter, listWidth)
	listRows := append([]string{prompt}, m.renderThemePickerRows(listWidth)...)
	if layout.Wide {
		previewWidth := layout.SideWidth
		if previewWidth > 2 {
			previewWidth -= 2
		}
		previewRows := renderThemePreviewRows(codextui.ThemePreviewRows(previewWidth), previewWidth, m.modal.themePicker.PreviewThemeID())
		builder.WriteString(joinThemePickerColumns(listRows, previewRows, listWidth, previewWidth))
	} else {
		for _, row := range listRows {
			builder.WriteString(row)
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
		for _, row := range renderThemePreviewRows(codextui.NarrowThemePreviewRows(contentWidth), contentWidth, m.modal.themePicker.PreviewThemeID()) {
			builder.WriteString(row)
			builder.WriteString("\n")
		}
	}
	builder.WriteString("\n")
	builder.WriteString(renderThemePickerDimLine("Press enter to confirm or esc to go back", contentWidth))
	return strings.TrimRight(builder.String(), "\n")
}

func (m *Model) renderThemePickerRows(width int) []string {
	if m == nil || m.modal == nil || m.modal.themePicker == nil {
		return nil
	}
	picker := m.modal.themePicker
	indices := picker.FilteredIndices(m.modal.themeFilter)
	if len(indices) == 0 {
		return []string{renderThemePickerDimLine("  no matches", width)}
	}
	visible := themePickerVisibleIndices(indices, picker.Selected, themePickerListHeight(m.height))
	rows := make([]string, 0, len(visible))
	for _, index := range visible {
		if index < 0 || index >= len(picker.Themes) {
			continue
		}
		theme := picker.Themes[index]
		selected := index == picker.Selected
		row := codextui.SelectionPrefix(selected) + codextui.ThemePickerDisplayName(theme)
		if theme.ID == picker.Current {
			row += " (current)"
		}
		if width > 0 {
			row = codextui.TruncateWithEllipsis(row, width)
		}
		if selected {
			row = codextui.RenderSelectedRow(row)
		}
		rows = append(rows, row)
	}
	return rows
}

func themePickerVisibleIndices(indices []int, selected int, maxRows int) []int {
	if maxRows <= 0 || len(indices) <= maxRows {
		return append([]int(nil), indices...)
	}
	position := 0
	for i, index := range indices {
		if index == selected {
			position = i
			break
		}
	}
	start := position - maxRows/2
	if start < 0 {
		start = 0
	}
	if start+maxRows > len(indices) {
		start = len(indices) - maxRows
	}
	return append([]int(nil), indices[start:start+maxRows]...)
}

func themePickerListHeight(height int) int {
	if height <= 0 {
		return 12
	}
	rows := height - 10
	if rows < 8 {
		return 8
	}
	if rows > 18 {
		return 18
	}
	return rows
}

func renderThemePickerSearchPrompt(filter string, width int) string {
	text := "Type to filter themes..."
	if strings.TrimSpace(filter) != "" {
		text = "Filter: " + filter
	}
	return renderThemePickerDimLine(codextui.TruncateWithEllipsis(text, width), width)
}

func renderThemePickerDimLine(text string, width int) string {
	if width > 0 {
		text = codextui.TruncateWithEllipsis(text, width)
	}
	return codextui.ForcedColorStyle().Foreground(lipgloss.Color("8")).Render(text)
}

func renderThemePreviewRows(rows []string, width int, themeID string) []string {
	out := make([]string, 0, len(rows))
	palette := codextui.ThemePreviewPaletteForID(themeID)
	contextStyle := codextui.ForcedColorStyle().Foreground(lipgloss.Color(palette.Foreground))
	addStyle := codextui.ForcedColorStyle().
		Foreground(lipgloss.Color(palette.InsertForeground)).
		Background(lipgloss.Color(palette.InsertBackground))
	removeStyle := codextui.ForcedColorStyle().
		Foreground(lipgloss.Color(palette.DeleteForeground)).
		Background(lipgloss.Color(palette.DeleteBackground))
	for _, row := range rows {
		if width > 0 {
			row = codextui.TruncateWithEllipsis(row, width)
		}
		switch {
		case strings.Contains(row, " + "):
			row = addStyle.Render(row)
		case strings.Contains(row, " - "):
			row = removeStyle.Render(row)
		default:
			row = contextStyle.Render(row)
		}
		out = append(out, row)
	}
	return out
}

func joinThemePickerColumns(left []string, right []string, listWidth int, sideWidth int) string {
	rows := len(left)
	if len(right) > rows {
		rows = len(right)
	}
	var builder strings.Builder
	for i := 0; i < rows; i++ {
		leftLine := ""
		if i < len(left) {
			leftLine = left[i]
		}
		rightLine := ""
		if i < len(right) {
			rightLine = right[i]
		}
		builder.WriteString(padRightDisplayStyled(leftLine, listWidth))
		if strings.TrimSpace(utils.StripANSI(rightLine)) != "" {
			builder.WriteString("  ")
			builder.WriteString(rightLine)
		}
		if i+1 < rows {
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

func padRightDisplayStyled(value string, width int) string {
	if width <= 0 {
		return ""
	}
	padding := width - codextui.DisplayWidth(utils.StripANSI(value))
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}
