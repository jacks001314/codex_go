package tea

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/config"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// Rust parity: codex-rs/tui/src/chatwidget/quick_dials.rs and
// codex-rs/tui/src/app/quick_dials.rs. Presets are numbered, reusable prompts
// or slash commands stored under `tui.presets.<slot>`.

const (
	quickDialEditorID         = "quick-dials-editor"
	quickDialSavePickerID     = "quick-dials-save-picker"
	quickDialPrefillPickerID  = "quick-dials-prefills"
	quickDialEditPickerID     = "quick-dials-edit-picker"
	quickDialDeletePickerID   = "quick-dials-delete-picker"
	quickDialReplaceConfirmID = "quick-dial-replace-confirm"
	quickDialDeleteConfirmID  = "quick-dial-delete-confirm"
)

var quickDialSlotOrder = []byte{'1', '2', '3', '4', '5', '6', '7', '8', '9', '0'}

// quickDialKeyGuardWindow mirrors Rust's ActivationKeyGuard legacy repeat
// window: terminals without release events report a held key as fresh presses.
const quickDialKeyGuardWindow = time.Second

func cloneTUIPresets(values map[string]config.TUIPreset) map[string]config.TUIPreset {
	if values == nil {
		return nil
	}
	out := make(map[string]config.TUIPreset, len(values))
	for key, preset := range values {
		if preset.Label != nil {
			label := *preset.Label
			preset.Label = &label
		}
		out[key] = preset
	}
	return out
}

// quickDialLabel mirrors Rust's label fallback: a non-blank label wins,
// otherwise the first line of the input is shown.
func quickDialLabel(preset config.TUIPreset) string {
	text := preset.Input
	if preset.Label != nil && strings.TrimSpace(*preset.Label) != "" {
		text = *preset.Label
	}
	text = strings.TrimLeftFunc(text, unicode.IsSpace)
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	return strings.TrimSpace(text)
}

func (m *Model) quickDialsAvailable() bool {
	if m == nil {
		return false
	}
	if m.readOnlyThread || m.inSideConversation() {
		m.notice = "Presets are unavailable in this conversation."
		m.refreshTranscript()
		return false
	}
	if m.currentThreadID() == "" {
		m.notice = "Presets are available after startup completes."
		m.refreshTranscript()
		return false
	}
	return true
}

func (m *Model) openQuickDialMenu() bubbletea.Cmd {
	if m == nil || !m.quickDialsAvailable() {
		return nil
	}
	// Rust #54381: the HUD yields while another modal or popup owns input.
	if m.modal != nil || m.overlay != nil || m.agentsOverview != nil || m.windowsSandboxSetupActive ||
		m.slashPopup.Active || m.skillPopup.Active {
		return nil
	}
	m.quickDialMenu = true
	m.notice = ""
	return nil
}

// consumeQuickDialKeyGuard reports whether keySpec is a held repeat of the key
// that just triggered a preset-menu action (Rust #54381
// quick_dial_menu_key_guard).
func (m *Model) consumeQuickDialKeyGuard(keySpec string) bool {
	if m == nil || m.quickDialGuardSpec == "" || strings.TrimSpace(keySpec) == "" {
		return false
	}
	if keySpec != m.quickDialGuardSpec {
		m.quickDialGuardSpec = ""
		m.quickDialGuardUntil = time.Time{}
		return false
	}
	now := m.currentTime()
	if !now.Before(m.quickDialGuardUntil) {
		m.quickDialGuardSpec = ""
		m.quickDialGuardUntil = time.Time{}
		return false
	}
	m.quickDialGuardUntil = now.Add(quickDialKeyGuardWindow)
	return true
}

// armQuickDialKeyGuard suppresses auto-repeats of a consumed menu action.
func (m *Model) armQuickDialKeyGuard(keySpec string) {
	if m == nil || strings.TrimSpace(keySpec) == "" {
		return
	}
	m.quickDialGuardSpec = keySpec
	m.quickDialGuardUntil = m.currentTime().Add(quickDialKeyGuardWindow)
}

func (m *Model) renderQuickDialHUD() string {
	if m == nil || !m.quickDialMenu {
		return ""
	}
	entries := make([]string, 0, len(quickDialSlotOrder))
	for _, slot := range quickDialSlotOrder {
		preset, ok := m.quickDials[string(slot)]
		if !ok {
			continue
		}
		entries = append(entries, string(slot)+" · "+quickDialLabel(preset))
	}

	width := m.width
	if width <= 0 {
		width = 80
	}
	lines := []string{"Presets", ""}
	if len(entries) == 0 {
		lines = append(lines, "No presets saved. Press n to create one.")
	} else {
		columns := 1
		if len(entries) > 5 && width >= 66 {
			columns = 2
		}
		lines = append(lines, layoutQuickDialEntries(entries, width, columns)...)
	}
	lines = append(lines, "", "n new   s from settings   e edit   d delete   esc close")
	return strings.Join(lines, "\n")
}

func layoutQuickDialEntries(entries []string, width, columns int) []string {
	if columns < 2 {
		lines := make([]string, 0, len(entries))
		for _, entry := range entries {
			lines = append(lines, fitTerminalLine(entry, width))
		}
		return lines
	}
	cellWidth := (width - 2) / columns
	if cellWidth < 1 {
		cellWidth = 1
	}
	rows := (len(entries) + columns - 1) / columns
	lines := make([]string, 0, rows)
	for row := 0; row < rows; row++ {
		var line strings.Builder
		for column := 0; column < columns; column++ {
			index := column*rows + row
			if index >= len(entries) {
				break
			}
			if column > 0 {
				line.WriteString("  ")
			}
			cell := fitTerminalLine(entries[index], cellWidth)
			line.WriteString(cell)
			if column+1 < columns {
				if padding := cellWidth - len([]rune(cell)); padding > 0 {
					line.WriteString(strings.Repeat(" ", padding))
				}
			}
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return lines
}

func (m *Model) updateQuickDialMenuKey(message bubbletea.KeyMsg) (bubbletea.Cmd, bool) {
	if m == nil || !m.quickDialMenu {
		return nil, false
	}
	switch message.Type {
	case bubbletea.KeyEsc:
		m.quickDialMenu = false
		m.armQuickDialKeyGuard(keySpecFromKeyMsg(message))
		return nil, true
	case bubbletea.KeyEnter:
		// Rust #54381 consumes Enter without dismissing the passive HUD.
		return nil, true
	case bubbletea.KeyRunes:
		if message.Alt {
			break
		}
		text := string(message.Runes)
		runes := []rune(text)
		if len(runes) != 1 {
			break
		}
		switch ch := runes[0]; ch {
		case 'n':
			m.quickDialMenu = false
			m.armQuickDialKeyGuard(keySpecFromKeyMsg(message))
			return m.openQuickDialEditor(-1, m.quickDialComposerText()), true
		case 's':
			m.quickDialMenu = false
			m.armQuickDialKeyGuard(keySpecFromKeyMsg(message))
			return m.openQuickDialPrefills(), true
		case 'e':
			m.quickDialMenu = false
			m.armQuickDialKeyGuard(keySpecFromKeyMsg(message))
			m.openQuickDialsPicker(quickDialEditPickerID, "Edit a preset")
			return nil, true
		case 'd':
			m.quickDialMenu = false
			m.armQuickDialKeyGuard(keySpecFromKeyMsg(message))
			m.openQuickDialsPicker(quickDialDeletePickerID, "Delete a preset")
			return nil, true
		default:
			if ch >= '0' && ch <= '9' {
				slot := byte(ch)
				m.quickDialMenu = false
				m.armQuickDialKeyGuard(keySpecFromKeyMsg(message))
				if _, ok := m.quickDials[string(slot)]; ok {
					return m.recallQuickDial(slot), true
				}
				return m.openQuickDialEditor(int(slot-'0'), m.quickDialComposerText()), true
			}
		}
	}
	m.quickDialMenu = false
	return nil, false
}

// quickDialComposerText mirrors Rust's composer_quick_dial_text: attachments
// and linked mentions cannot be represented in a preset, so the editor starts
// with empty text and explains why instead of copying an incomplete draft.
func (m *Model) quickDialComposerText() string {
	if m == nil {
		return ""
	}
	if len(m.attachments) > 0 {
		m.addInfoHistoryMessage("Images cannot be copied into a preset. Starting with empty text.")
		return ""
	}
	if len(m.composerMentionBindings) > 0 {
		m.addInfoHistoryMessage("Linked mentions cannot be copied into a preset. Starting with empty text.")
		return ""
	}
	return m.composerText()
}

func (m *Model) openQuickDialPrefills() bubbletea.Cmd {
	if m == nil || !m.quickDialsAvailable() {
		return nil
	}
	modelPreset, modelReason := m.currentModelQuickDial()
	permissionPreset, permissionReason := m.currentPermissionsQuickDial()
	titlePreset, titleReason := m.currentTerminalTitleQuickDial()
	statusPreset, statusReason := m.currentStatusLineQuickDial()
	type prefill struct {
		id     string
		label  string
		preset config.TUIPreset
		reason string
	}
	entries := []prefill{
		{id: "model", label: "Current model", preset: modelPreset, reason: modelReason},
		{id: "permissions", label: "Current permissions", preset: permissionPreset, reason: permissionReason},
		{id: "terminal-title", label: "Current terminal title", preset: titlePreset, reason: titleReason},
		{id: "status-line", label: "Current status line", preset: statusPreset, reason: statusReason},
	}
	m.quickDialPrefills = map[string]config.TUIPreset{}
	options := make([]ModalOption, 0, len(entries))
	for _, entry := range entries {
		option := ModalOption{ID: entry.id, Label: entry.label}
		if entry.reason != "" {
			option.Disabled = true
			option.DisabledReason = entry.reason
			option.Description = entry.reason
		} else {
			m.quickDialPrefills[entry.id] = entry.preset
			option.Description = entry.preset.Input
		}
		options = append(options, option)
	}
	m.openModal(ModalRequestMsg{
		ID:      quickDialPrefillPickerID,
		Kind:    ModalKindPresets,
		Title:   "Start preset from current settings",
		Options: options,
	})
	return nil
}

func (m *Model) currentModelQuickDial() (config.TUIPreset, string) {
	if m == nil || m.State == nil {
		return config.TUIPreset{}, "Session is still starting; try /presets again in a moment."
	}
	model := strings.TrimSpace(m.State.Model)
	if model == "" {
		return config.TUIPreset{}, "Choose a model with /model first."
	}
	input := "/model " + quickDialQuote(model)
	if effort := strings.TrimSpace(m.State.EffectiveReasoningEffort()); effort != "" {
		input += " --reasoning=" + quickDialQuote(effort)
	}
	speed := strings.TrimSpace(m.State.ServiceTier)
	if speed == "" || speed == codextui.ServiceTierDefaultRequestValue {
		speed = "standard"
	}
	input += " --speed=" + quickDialQuote(speed)
	if m.currentThreadID() != "" {
		if _, failure := m.resolveModelCommandArgs(strings.TrimPrefix(input, "/model ")); failure != "" {
			return config.TUIPreset{}, failure
		}
	}
	return quickDialPreset(input)
}

func (m *Model) currentPermissionsQuickDial() (config.TUIPreset, string) {
	if m == nil || m.State == nil {
		return config.TUIPreset{}, "Session is still starting; try /presets again in a moment."
	}
	profile := strings.TrimSpace(m.State.Sandbox)
	if profile == "" {
		return config.TUIPreset{}, "Select a named permission profile with /permissions first."
	}
	policy := strings.TrimSpace(m.State.ApprovalPolicy)
	if policy == "" {
		policy = string(currentPermissionApprovalPolicy(m))
	}
	if policy == "granular" {
		return config.TUIPreset{}, "The approval policy cannot be recalled."
	}
	reviewer := string(currentApprovalsReviewer(m))
	input := "/permissions " + quickDialQuote(profile)
	if policy != "" {
		input += " --approval-policy=" + quickDialQuote(policy)
	}
	if reviewer != "" {
		input += " --reviewer=" + quickDialQuote(reviewer)
	}
	return quickDialPreset(input)
}

func (m *Model) currentTerminalTitleQuickDial() (config.TUIPreset, string) {
	if m == nil {
		return config.TUIPreset{}, "Terminal title is unavailable."
	}
	items := m.statusControls.ConfiguredTerminalTitleItems()
	normalized := make([]string, 0, len(items))
	for _, id := range items {
		item, ok := bottompane.ParseTerminalTitleItem(id)
		if !ok {
			return config.TUIPreset{}, "Choose available terminal-title items with /title before saving."
		}
		normalized = append(normalized, string(item))
	}
	input := "/title"
	if len(normalized) == 0 {
		input += " --clear"
	} else {
		for _, item := range normalized {
			input += " " + quickDialQuote(item)
		}
	}
	return quickDialPreset(input)
}

func (m *Model) currentStatusLineQuickDial() (config.TUIPreset, string) {
	if m == nil {
		return config.TUIPreset{}, "Status line is unavailable."
	}
	items := m.statusControls.ConfiguredStatusLineItems()
	normalized := make([]string, 0, len(items))
	for _, id := range items {
		item, ok := bottompane.ParseStatusLineItem(id)
		if !ok {
			return config.TUIPreset{}, "Choose available status-line items with /statusline before saving."
		}
		normalized = append(normalized, bottompane.StatusLineItemID(item))
	}
	input := "/statusline"
	if len(normalized) == 0 {
		input += " --clear"
	} else {
		for _, item := range normalized {
			input += " " + quickDialQuote(item)
		}
	}
	if m.statusLineUseColors {
		input += " --colors"
	} else {
		input += " --no-colors"
	}
	return quickDialPreset(input)
}

func quickDialQuote(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\"'") {
		return value
	}
	return strconv.Quote(value)
}

func quickDialPreset(input string) (config.TUIPreset, string) {
	preset := config.TUIPreset{Input: input}
	if err := preset.Validate(); err != nil {
		return config.TUIPreset{}, err.Error()
	}
	return preset, ""
}

func (m *Model) recallQuickDial(slot byte) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	preset, ok := m.quickDials[string(slot)]
	if !ok {
		return nil
	}
	if err := preset.Validate(); err != nil {
		m.notice = "Preset " + string(slot) + ": " + err.Error()
		m.refreshTranscript()
		return nil
	}
	input := preset.Input
	if invocation, ok := codextui.ParseCommand(input); ok && slashInvocationDispatchable(invocation) {
		if invocation.Command == codextui.CommandReview && m.reviewBlockedDuringForegroundWork() {
			m.showReviewBusyError()
			return nil
		}
		if m.isTaskRunning() && !commandAvailableDuringTask(invocation.Command) {
			message := "'" + strings.TrimSpace(invocation.Name) + "' is disabled while a task is in progress."
			m.State.AddHistoryLines([]string{message}, []string{message})
			m.notice = message
			m.refreshTranscript()
			return nil
		}
		return m.applyCommand(invocation)
	}
	// Queue the preset without replacing the live composer draft, mirroring
	// Rust RecallQuickDial -> queue_user_message_with_options(ParseSlash).
	return m.submitRequest(SubmitRequest{Prompt: input}, false)
}

func (m *Model) openQuickDialEditor(slot int, initial string) bubbletea.Cmd {
	if m == nil || !m.quickDialsAvailable() {
		return nil
	}
	if slot < -1 || slot > 9 {
		return nil
	}
	var label *string
	title := "New preset"
	if slot >= 0 {
		key := strconv.Itoa(slot)
		if existing, ok := m.quickDials[key]; ok {
			title = "Edit preset " + key
			if existing.Label != nil {
				value := *existing.Label
				label = &value
			}
		}
	}
	view := bottompane.NewCustomPromptView(title, "Write a prompt or slash command", initial, "")
	view.SetVimEnabled(m.vimMode)
	view.PreserveWhitespace()
	m.quickDialMenu = false
	m.replaceModal(&modalState{
		id:           quickDialEditorID,
		kind:         ModalKindPresets,
		title:        title,
		customPrompt: view,
		customPromptSubmit: func(input string) bubbletea.Cmd {
			if slot < 0 {
				return m.openQuickDialSavePicker(input, label)
			}
			return m.saveQuickDial(slot, input, label)
		},
	})
	m.notice = ""
	return nil
}

// openQuickDialSavePicker mirrors Rust's "Save preset to slot" picker. Empty
// slots save directly; selecting an occupied slot asks for replacement
// confirmation.
func (m *Model) openQuickDialSavePicker(input string, label *string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	preset := config.TUIPreset{Input: input, Label: label}
	if err := preset.Validate(); err != nil {
		m.openQuickDialEditor(-1, input)
		m.notice = err.Error()
		m.refreshTranscript()
		return nil
	}
	m.quickDialPending = &preset
	options := make([]ModalOption, 0, len(quickDialSlotOrder))
	selected := -1
	for index, slot := range quickDialSlotOrder {
		key := string(slot)
		if existing, ok := m.quickDials[key]; ok {
			options = append(options, ModalOption{
				ID:          key,
				Label:       key + " · " + quickDialLabel(existing),
				Description: quickDialLabel(preset),
			})
			continue
		}
		if selected < 0 {
			selected = index
		}
		options = append(options, ModalOption{
			ID:          key,
			Label:       key + " · empty",
			Description: quickDialLabel(preset),
		})
	}
	m.openModal(ModalRequestMsg{
		ID:      quickDialSavePickerID,
		Kind:    ModalKindPresets,
		Title:   "Save preset to slot",
		Options: options,
	})
	if m.modal != nil {
		if selected < 0 {
			selected = 0
		}
		m.modal.selected = selected
	}
	return nil
}

func (m *Model) openQuickDialReplaceConfirmation(slot int, preset *config.TUIPreset) bubbletea.Cmd {
	if m == nil || preset == nil {
		return nil
	}
	key := strconv.Itoa(slot)
	m.quickDialPending = preset
	body := quickDialLabel(*preset)
	if existing, ok := m.quickDials[key]; ok {
		body = quickDialLabel(existing) + "\n→ " + body
	}
	m.openModal(ModalRequestMsg{
		ID:    quickDialReplaceConfirmID,
		Kind:  ModalKindPresets,
		Title: "Replace preset " + key + "?",
		Body:  body,
		Options: []ModalOption{{
			ID:                  key,
			Label:               "Replace preset " + key,
			RequireConfirmation: true,
		}},
	})
	return nil
}

func (m *Model) openQuickDialsPicker(id string, title string) {
	if m == nil || !m.quickDialsAvailable() {
		return
	}
	options := make([]ModalOption, 0, len(quickDialSlotOrder))
	for _, slot := range quickDialSlotOrder {
		preset, ok := m.quickDials[string(slot)]
		if !ok {
			continue
		}
		options = append(options, ModalOption{
			ID:          string(slot),
			Label:       string(slot) + " · " + quickDialLabel(preset),
			Description: strings.TrimSpace(preset.Input),
		})
	}
	if len(options) == 0 {
		m.notice = "No presets saved."
		m.refreshTranscript()
		return
	}
	m.openModal(ModalRequestMsg{
		ID:      id,
		Kind:    ModalKindPresets,
		Title:   title,
		Options: options,
	})
}

func (m *Model) applyQuickDialModalOption(id string, optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if id == quickDialPrefillPickerID {
		preset, ok := m.quickDialPrefills[optionID]
		if !ok {
			return nil
		}
		m.quickDialPrefills = nil
		return m.openQuickDialEditor(-1, preset.Input)
	}
	slot, err := strconv.Atoi(strings.TrimSpace(optionID))
	if err != nil || slot < 0 || slot > 9 {
		return nil
	}
	key := strconv.Itoa(slot)
	switch id {
	case quickDialSavePickerID:
		pending := m.quickDialPending
		if pending == nil {
			return nil
		}
		if _, ok := m.quickDials[key]; ok {
			return m.openQuickDialReplaceConfirmation(slot, pending)
		}
		m.quickDialPending = nil
		return m.writeQuickDial(slot, pending)
	case quickDialEditPickerID:
		preset, ok := m.quickDials[key]
		if !ok {
			return nil
		}
		return m.openQuickDialEditor(slot, preset.Input)
	case quickDialDeletePickerID:
		return m.openQuickDialDeleteConfirmation(slot)
	case quickDialReplaceConfirmID:
		pending := m.quickDialPending
		if pending == nil {
			return nil
		}
		m.quickDialPending = nil
		return m.writeQuickDial(slot, pending)
	case quickDialDeleteConfirmID:
		return m.deleteQuickDial(slot)
	default:
		return nil
	}
}

func (m *Model) openQuickDialDeleteConfirmation(slot int) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	key := strconv.Itoa(slot)
	preset, ok := m.quickDials[key]
	if !ok {
		return nil
	}
	m.openModal(ModalRequestMsg{
		ID:    quickDialDeleteConfirmID,
		Kind:  ModalKindPresets,
		Title: "Delete preset " + key + "?",
		Body:  quickDialLabel(preset),
		Options: []ModalOption{{
			ID:                  key,
			Label:               "Delete preset " + key,
			RequireConfirmation: true,
		}},
	})
	return nil
}

func (m *Model) saveQuickDial(slot int, input string, label *string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	preset := config.TUIPreset{Input: input, Label: label}
	if err := preset.Validate(); err != nil {
		m.notice = err.Error()
		m.refreshTranscript()
		return nil
	}
	if _, ok := m.quickDials[strconv.Itoa(slot)]; ok {
		return m.openQuickDialReplaceConfirmation(slot, &preset)
	}
	return m.writeQuickDial(slot, &preset)
}

func (m *Model) deleteQuickDial(slot int) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.quickDialPending = nil
	return m.writeQuickDial(slot, nil)
}

func (m *Model) writeQuickDial(slot int, preset *config.TUIPreset) bubbletea.Cmd {
	if m == nil || slot < 0 || slot > 9 {
		return nil
	}
	key := strconv.Itoa(slot)
	if preset != nil && m.presetSetReadOnly[key] {
		m.quickDialPending = nil
		cmd := m.openQuickDialEditor(slot, preset.Input)
		m.notice = fmt.Sprintf("Preset %d is defined outside your user config. Edit its source configuration instead.", slot)
		m.refreshTranscript()
		return cmd
	}
	if preset == nil && m.presetClearReadOnly[key] {
		m.quickDialPending = nil
		m.notice = fmt.Sprintf("Preset %d is defined outside your user config. Edit its source configuration instead.", slot)
		m.refreshTranscript()
		return nil
	}
	if m.onWriteSettings == nil {
		m.notice = "Saving presets requires configuration write support."
		m.refreshTranscript()
		return nil
	}
	var value any
	if preset != nil {
		label := ""
		if preset.Label != nil {
			label = *preset.Label
		}
		value = map[string]any{"input": preset.Input, "label": label}
	}
	m.quickDialWriteSlot = slot
	m.quickDialWriteDelete = preset == nil
	if preset == nil {
		m.notice = "Deleting preset " + key + "…"
	} else {
		m.notice = "Saving preset " + key + "…"
	}
	m.refreshTranscript()
	return m.writeSettings(settingsWriteKindQuickDial, []SettingsEdit{{
		KeyPath: "tui.presets." + key,
		Value:   value,
	}})
}
