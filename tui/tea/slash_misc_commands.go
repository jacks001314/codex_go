package tea

import (
	_ "embed"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/features"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

func (m *Model) applyStatusCommand() bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	card := m.State.RenderStatusCardWidth(max(44, m.width-2))
	m.State.AddHistoryLines(strings.Split(card, "\n"), strings.Split(card, "\n"))
	m.notice = ""
	m.refreshTranscript()
	if !m.State.HasChatGPTAccount || m.onReadRateLimits == nil {
		return nil
	}
	requestID := m.nextStatusRateLimitRequestID
	m.nextStatusRateLimitRequestID++
	if m.pendingStatusRateLimitRequests == nil {
		m.pendingStatusRateLimitRequests = map[uint64]struct{}{}
	}
	m.pendingStatusRateLimitRequests[requestID] = struct{}{}
	reader := m.onReadRateLimits
	return func() bubbletea.Msg {
		limits, err := reader()
		return RateLimitsResultMsg{RequestID: requestID, Limits: limits, Err: err}
	}
}

func (m *Model) applyRateLimitsResult(message RateLimitsResultMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if _, pending := m.pendingStatusRateLimitRequests[message.RequestID]; !pending {
		return nil
	}
	delete(m.pendingStatusRateLimitRequests, message.RequestID)
	if message.Err != nil || len(message.Limits) == 0 {
		return nil
	}
	m.State.RateLimits = append([]codextui.RateLimitStatus(nil), message.Limits...)
	return m.refreshStatusControlsCmd()
}

//go:embed prompt_for_init_command.md
var initCommandPromptText string

func (m *Model) startFreshNamedSession(args string, defaultNotice string) {
	if m == nil || m.State == nil {
		return
	}
	name := strings.TrimSpace(args)
	m.State.ResetThread()
	m.State.SetThreadName(name)
	m.pendingThreadName = name != ""
	if name == "" {
		m.notice = defaultNotice
	} else {
		m.notice = "Started a new session named " + name + "."
	}
	m.refreshTranscript()
}

func (m *Model) applyPlanCommand(args string) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if !features.Enabled(m.featureSettings, "collaboration_modes") {
		m.applyHistoryCell(historycell.NewInfoEvent("Collaboration modes are disabled.", "Enable collaboration modes to use /plan."))
		m.notice = ""
		return nil
	}
	if _, ok := chatwidget.PlanCollaborationMask(nil); !ok {
		m.applyHistoryCell(historycell.NewInfoEvent("Plan mode unavailable right now.", ""))
		m.notice = ""
		return nil
	}
	previousMode := m.State.PlanMode
	previousEffort := m.State.EffectiveReasoningEffort()
	m.State.PlanMode = true
	m.notice = ""
	if !previousMode && strings.TrimSpace(previousEffort) != "" && previousEffort != m.State.EffectiveReasoningEffort() {
		message := "Model changed to " + firstNonEmpty(strings.TrimSpace(m.State.Model), "default") + " " + firstNonEmpty(m.State.EffectiveReasoningEffort(), "default") + " for Plan mode."
		m.applyHistoryCell(historycell.NewInfoEvent(message, ""))
	}
	mode := m.effectiveSubmissionCollaborationMode()
	if mode != nil && strings.TrimSpace(m.State.ThreadID) != "" && m.onUpdateCollaborationMode != nil {
		if err := m.onUpdateCollaborationMode(m.State.ThreadID, *mode); err != nil {
			m.addErrorHistoryMessage("Failed to switch to Plan mode: " + err.Error())
		}
	}
	if strings.TrimSpace(args) != "" {
		return m.submitRequest(SubmitRequest{Prompt: strings.TrimSpace(args), CollaborationMode: mode}, false)
	}
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
}

func (m *Model) effectiveSubmissionCollaborationMode() *chatwidget.CollaborationMode {
	if m == nil || m.State == nil || !features.Enabled(m.featureSettings, "collaboration_modes") {
		return nil
	}
	kind := chatwidget.CollaborationModeKindDefault
	if m.State.PlanMode {
		kind = chatwidget.CollaborationModeKindPlan
	}
	mode := chatwidget.NewCollaborationMode(
		kind,
		m.State.Model,
		m.State.EffectiveReasoningEffort(),
		chatwidget.CollaborationModeInstructions(kind),
	)
	return &mode
}

func (m *Model) applyReviewCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if strings.TrimSpace(args) != "" {
		target, ok := chatwidget.ReviewTargetForCustomPrompt(args)
		if !ok {
			m.notice = "Usage: /review <custom instructions>"
			m.refreshTranscript()
			return nil
		}
		return m.startReview(target)
	}
	m.openSelectionViewModal(ModalKindReview, chatwidget.NewReviewPresetView())
	return nil
}

func (m *Model) applyReviewModalOption(optionID string, optionLabel string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch chatwidget.UsageMenuAction(optionID) {
	case chatwidget.ReviewActionUncommitted:
		return m.startReview(chatwidget.ReviewTargetForUncommittedChanges())
	case chatwidget.ReviewActionOpenBranchPicker:
		return m.applyReviewBranchPickerCommand()
	case chatwidget.ReviewActionOpenCommitPicker:
		return m.applyReviewCommitPickerCommand()
	case chatwidget.ReviewActionOpenCustomPrompt:
		m.openReviewCustomPrompt()
		return nil
	default:
		if branch, ok := strings.CutPrefix(optionID, "review_base_branch:"); ok {
			target, targetOK := chatwidget.ReviewTargetForBranch(branch)
			if targetOK {
				return m.startReview(target)
			}
		}
		if sha, ok := strings.CutPrefix(optionID, "review_commit:"); ok {
			target, targetOK := chatwidget.ReviewTargetForCommit(chatwidget.ReviewCommitEntry{Subject: optionLabel, SHA: sha})
			if targetOK {
				return m.startReview(target)
			}
		}
		m.notice = "Review"
		m.refreshTranscript()
		return nil
	}
}

func (m *Model) openReviewCustomPrompt() {
	if m == nil {
		return
	}
	view := chatwidget.NewReviewCustomPromptView()
	contextLabel := ""
	if view.ContextLabel != nil {
		contextLabel = strings.TrimSpace(*view.ContextLabel)
	}
	prompt := bottompane.NewCustomPromptView(view.Title, view.Placeholder, view.InitialText, contextLabel)
	m.modal = &modalState{
		id:           "review-custom-prompt",
		kind:         ModalKindReview,
		customPrompt: prompt,
		customPromptSubmit: func(instructions string) bubbletea.Cmd {
			target, ok := chatwidget.ReviewTargetForCustomPrompt(instructions)
			if !ok {
				return nil
			}
			return m.startReview(target)
		},
	}
	m.notice = ""
}

func (m *Model) applyRenameCommand(args string) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	name := strings.TrimSpace(args)
	if name == "" {
		m.openRenamePrompt()
		return nil
	}
	return m.renameCurrentThread(name)
}

func (m *Model) openRenamePrompt() {
	if m == nil || m.State == nil {
		return
	}
	title := "Name thread"
	initial := strings.TrimSpace(m.State.ThreadName)
	if initial != "" {
		title = "Rename thread"
	}
	prompt := bottompane.NewCustomPromptView(title, "Type a name and press Enter", initial, "")
	m.modal = &modalState{
		id:           "rename-thread",
		kind:         ModalKindGeneric,
		customPrompt: prompt,
		customPromptSubmit: func(name string) bubbletea.Cmd {
			return m.renameCurrentThread(name)
		},
	}
	m.notice = ""
}

func (m *Model) renameCurrentThread(name string) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		m.addErrorHistoryMessage("Thread name cannot be empty.")
		m.refreshTranscript()
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		m.State.SetThreadName(name)
		m.pendingThreadName = true
		m.notice = "Thread will be named " + name + " when it starts."
		m.refreshTranscript()
		return nil
	}
	if m.onRenameThread != nil {
		if err := m.onRenameThread(threadID, name); err != nil {
			message := "Failed to rename thread: " + err.Error()
			m.notice = message
			m.addErrorHistoryMessage(message)
			m.refreshTranscript()
			return nil
		}
	}
	m.State.SetThreadName(name)
	m.pendingThreadName = false
	message := "Thread renamed to " + name + "."
	m.notice = message
	m.addInfoHistoryMessage(message)
	m.refreshTranscript()
	return nil
}

func (m *Model) persistPendingThreadName() {
	if m == nil || m.State == nil || !m.pendingThreadName {
		return
	}
	name := strings.TrimSpace(m.State.ThreadName)
	threadID := strings.TrimSpace(m.State.ThreadID)
	if name == "" || threadID == "" {
		return
	}
	if m.onRenameThread != nil {
		if err := m.onRenameThread(threadID, name); err != nil {
			message := "Failed to name thread: " + err.Error()
			m.notice = message
			m.addErrorHistoryMessage(message)
			m.pendingThreadName = false
			m.refreshTranscript()
			return
		}
	}
	m.pendingThreadName = false
}

func (m *Model) applyLogoutCommand() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.onLogout == nil {
		message := "Logout failed: logout is unavailable"
		m.notice = message
		m.addErrorHistoryMessage(message)
		m.refreshTranscript()
		return nil
	}
	m.notice = "Logging out..."
	m.refreshTranscript()
	logout := m.onLogout
	return func() bubbletea.Msg {
		return LogoutResultMsg{Err: logout()}
	}
}

func (m *Model) applyLogoutResult(message LogoutResultMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if message.Err != nil {
		text := "Logout failed: " + message.Err.Error()
		m.notice = text
		m.addErrorHistoryMessage(text)
		m.refreshTranscript()
		return nil
	}
	return bubbletea.Quit
}

func (m *Model) openSkillsMenu() {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindSkills, chatwidget.NewSkillsMenuView(features.Enabled(m.featureSettings, "mentions_v2")))
}

func (m *Model) applySkillsModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch chatwidget.UsageMenuAction(optionID) {
	case chatwidget.SkillsMenuActionList:
		m.composer.InsertString(chatwidget.OpenSkillsListInsert(features.Enabled(m.featureSettings, "mentions_v2")))
		m.notice = "Skills list shortcut inserted."
		cmd := m.refreshSkillPopup()
		m.refreshTranscript()
		return cmd
	case chatwidget.SkillsMenuActionManage:
		return m.applySkillsManageCommand()
	default:
		m.notice = "Skills"
	}
	m.refreshTranscript()
	return nil
}

func (m *Model) openAgentPickerPlaceholder() {
	if m == nil {
		return
	}
	m.openModal(ModalRequestMsg{
		ID:    "agent-picker",
		Kind:  ModalKindGeneric,
		Title: "Agents",
		Body:  "No agent threads are available in this runtime.",
		Options: []ModalOption{{
			ID:    "ok",
			Label: "OK",
		}},
	})
}

func (m *Model) applyThemeCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	themeID := strings.TrimSpace(args)
	if themeID == "" {
		m.openThemePicker()
		return nil
	}
	theme, ok := themeOptionByIDTea(themeID)
	if !ok {
		m.notice = "Unknown theme: " + themeID
		m.refreshTranscript()
		return nil
	}
	return m.setTUITheme(theme.ID)
}

func (m *Model) openThemePicker() {
	if m == nil {
		return
	}
	themeDir := codextui.DefaultThemeDir()
	options := codextui.DiscoverThemeOptions(codextui.BuiltinThemeIDs(), codextui.DiscoverCustomThemePaths(themeDir))
	currentTheme := effectiveThemeIDTea(m.tuiTheme)
	picker := codextui.NewThemePicker(options, currentTheme)
	m.modal = &modalState{
		id:            "theme",
		kind:          ModalKindTheme,
		title:         "Select Syntax Theme",
		themePicker:   picker,
		themeSubtitle: codextui.ThemePickerSubtitle(themeDir, m.width),
	}
	m.notice = ""
}

func (m *Model) applyThemeModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	theme, ok := themeOptionByIDTea(optionID)
	if !ok {
		m.notice = "Unknown theme: " + strings.TrimSpace(optionID)
		m.refreshTranscript()
		return nil
	}
	return m.setTUITheme(theme.ID)
}

func (m *Model) setTUITheme(themeID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	themeID = strings.TrimSpace(themeID)
	if themeID == "" {
		m.notice = "Usage: /theme"
		m.refreshTranscript()
		return nil
	}
	m.tuiTheme = themeID
	m.notice = "Theme set to " + themeLabelTea(themeID) + "."
	m.refreshTranscript()
	if m.onWriteSettings != nil {
		return m.writeSettings(settingsWriteKindTheme, []SettingsEdit{{
			KeyPath: "tui.theme",
			Value:   themeID,
		}})
	}
	return nil
}

func (m *Model) applyPetsCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	petID := normalizePetIDTea(args)
	if petID != "" {
		return m.setTUIPet(petID)
	}
	result := chatwidget.NewPetsPickerView(m.tuiPet, chatwidget.PetImageSupport{Kind: chatwidget.PetImageSupported}, chatwidget.BuiltinPetOptions())
	if strings.TrimSpace(result.InfoMessage) != "" {
		m.notice = result.InfoMessage
		m.refreshTranscript()
		return nil
	}
	m.openSelectionViewModal(ModalKindPets, result.View)
	return nil
}

func (m *Model) applyPetsModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	petID := normalizePetIDTea(optionID)
	if petID == "" {
		m.notice = "Pets"
		m.refreshTranscript()
		return nil
	}
	return m.setTUIPet(petID)
}

func (m *Model) setTUIPet(petID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	petID = normalizePetIDTea(petID)
	if petID == "" {
		petID = chatwidget.DefaultPetID
	}
	m.tuiPet = petID
	if petID == chatwidget.DisabledPetID {
		m.notice = "Terminal pets disabled."
	} else {
		m.notice = "Pet set to " + petLabelTea(petID) + "."
	}
	m.refreshTranscript()
	if m.onWriteSettings != nil {
		return m.writeSettings(settingsWriteKindPet, []SettingsEdit{{
			KeyPath: "tui.pet",
			Value:   petID,
		}})
	}
	return nil
}

func effectiveThemeIDTea(themeID string) string {
	themeID = strings.TrimSpace(themeID)
	if themeID != "" {
		if theme, ok := themeOptionByIDTea(themeID); ok {
			return theme.ID
		}
		return themeID
	}
	return "catppuccin-mocha"
}

func themeOptionByIDTea(themeID string) (codextui.ThemeOption, bool) {
	themeID = strings.TrimSpace(themeID)
	themeDir := codextui.DefaultThemeDir()
	for _, option := range codextui.DiscoverThemeOptions(codextui.BuiltinThemeIDs(), codextui.DiscoverCustomThemePaths(themeDir)) {
		if option.ID == themeID {
			return option, true
		}
	}
	return codextui.ThemeOption{}, false
}

func themeLabelTea(themeID string) string {
	theme, ok := themeOptionByIDTea(themeID)
	if ok {
		return theme.Label
	}
	return strings.TrimSpace(themeID)
}

func normalizePetIDTea(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "":
		return ""
	case "default":
		return chatwidget.DefaultPetID
	case "disable", "disabled", "hide", "hidden", "off", "none":
		return chatwidget.DisabledPetID
	default:
		return value
	}
}

func petLabelTea(petID string) string {
	petID = normalizePetIDTea(petID)
	if petID == chatwidget.DisabledPetID {
		return "Disable terminal pets"
	}
	for _, option := range chatwidget.BuiltinPetOptions() {
		if normalizePetIDTea(option.ID) == petID {
			return option.Name
		}
	}
	if petID == "" {
		return "Codex"
	}
	return petID
}

func (m *Model) toggleVimMode() {
	if m == nil {
		return
	}
	m.vimMode = !m.vimMode
	if m.vimMode {
		m.notice = "Vim mode enabled."
	} else {
		m.notice = "Vim mode disabled."
	}
	m.addInfoHistoryMessage(m.notice)
	m.refreshTranscript()
}

func (m *Model) openSelectionViewModal(kind ModalKind, view chatwidget.SelectionView) {
	if m == nil {
		return
	}
	options := make([]ModalOption, 0, len(view.Items))
	for _, item := range view.Items {
		id := item.ID
		if id == "" {
			id = string(item.Action)
		}
		if id == "" {
			id = item.Name
		}
		label := item.Name
		markers := []string{}
		if item.IsCurrent {
			markers = append(markers, "current")
		}
		if item.IsDefault {
			markers = append(markers, "default")
		}
		if len(markers) > 0 {
			label += " (" + strings.Join(markers, ", ") + ")"
		}
		options = append(options, ModalOption{
			ID:                   id,
			Label:                label,
			Description:          item.Description,
			SelectedDescription:  item.SelectedDescription,
			DisabledReason:       item.DisabledReason,
			DisabledGutterMarker: item.DisabledGutterMarker,
			Disabled:             item.Disabled || strings.TrimSpace(item.DisabledReason) != "",
		})
	}
	body := strings.TrimSpace(view.Subtitle)
	if len(view.HeaderLines) > 0 {
		header := strings.TrimSpace(strings.Join(view.HeaderLines, "\n"))
		if body != "" && header != "" {
			body = header + "\n" + body
		} else if header != "" {
			body = header
		}
	}
	m.openModal(ModalRequestMsg{
		ID:                view.ViewID,
		Kind:              kind,
		Title:             view.Title,
		Body:              body,
		Options:           options,
		FooterNote:        view.FooterNote,
		FooterHint:        view.FooterHint,
		ColumnWidth:       view.ColumnWidth,
		DescriptionLayout: view.DescriptionLayout,
	})
	if m.modal != nil && view.InitialSelectedIndex >= 0 && view.InitialSelectedIndex < len(m.modal.options) {
		m.modal.selected = view.InitialSelectedIndex
		if m.modal.options[m.modal.selected].Disabled {
			m.moveModalSelection(1)
		}
	}
}

func initCommandPrompt() string {
	return strings.TrimSpace(initCommandPromptText)
}

func newInfoHistoryCell(lines ...string) historycell.PlainHistoryCell {
	return historycell.NewPlainHistoryCell(lines)
}
