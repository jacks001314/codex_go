package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/internal/tui"
	chatwidget "codex_go/internal/tui/chatwidget"
	historycell "codex_go/internal/tui/history_cell"
)

func (m *Model) applyPlanCommand(args string) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	m.State.PlanMode = true
	m.notice = "Plan mode enabled."
	if strings.TrimSpace(args) != "" {
		return m.submitRequest(SubmitRequest{Prompt: strings.TrimSpace(args)}, false)
	}
	m.refreshTranscript()
	return m.refreshStatusControlsCmd()
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
		m.notice = "Usage: /review <custom instructions>"
		m.refreshTranscript()
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

func (m *Model) applyRenameCommand(args string) {
	if m == nil {
		return
	}
	name := strings.TrimSpace(args)
	if name == "" {
		m.notice = "Usage: /rename <name>"
		m.refreshTranscript()
		return
	}
	m.State.AddHistoryLines([]string{"Session renamed to " + name + "."}, []string{"Session renamed to " + name + "."})
	m.notice = "Session renamed to " + name + "."
	m.refreshTranscript()
}

func (m *Model) openSkillsMenu() {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindSkills, chatwidget.NewSkillsMenuView(true))
}

func (m *Model) applySkillsModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch chatwidget.UsageMenuAction(optionID) {
	case chatwidget.SkillsMenuActionList:
		m.composer.InsertString("$")
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
		description := item.Description
		if strings.TrimSpace(item.DisabledReason) != "" {
			if strings.TrimSpace(description) != "" {
				description += "\n"
			}
			description += item.DisabledReason
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
			ID:          id,
			Label:       label,
			Description: description,
			Disabled:    item.Disabled || strings.TrimSpace(item.DisabledReason) != "",
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
		ID:      view.ViewID,
		Kind:    kind,
		Title:   view.Title,
		Body:    body,
		Options: options,
	})
	if m.modal != nil && view.InitialSelectedIndex >= 0 && view.InitialSelectedIndex < len(m.modal.options) {
		m.modal.selected = view.InitialSelectedIndex
		if m.modal.options[m.modal.selected].Disabled {
			m.moveModalSelection(1)
		}
	}
}

func initCommandPrompt() string {
	return strings.TrimSpace(`Generate a file named AGENTS.md that serves as a contributor guide for this repository.

Your goal is to produce a clear, concise, and well-structured document with descriptive headings and actionable explanations for each section.

Title the document "Repository Guidelines". Keep explanations short, direct, and specific to this repository.`)
}

func newInfoHistoryCell(lines ...string) historycell.PlainHistoryCell {
	return historycell.NewPlainHistoryCell(lines)
}
