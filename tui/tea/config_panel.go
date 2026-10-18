package tea

import (
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

type configPanelSection int

const (
	configSectionAppearance configPanelSection = iota
	configSectionNotifications
	configSectionInput
	configSectionSessions
	configSectionTextSelection
	configSectionCount
)

var configPanelSectionTitles = []string{
	"Appearance",
	"Notifications",
	"Input",
	"Sessions",
	"Text selection",
}

type configPanelAction int

const (
	configActionTheme configPanelAction = iota
	configActionEffects
	configActionComposerStars
	configActionTips
	configActionStatusLineColors
	configActionStatusLine
	configActionTerminalTitle
	configActionNotifications
	configActionNotifyCondition
	configActionNotificationTest
	configActionKeyboard
	configActionStartInVimMode
	configActionPasteDetection
	configActionRightClickPaste
	configActionAutomaticRecaps
	configActionSessionPicker
	configActionResumeCWD
	configActionCopyOnSelect
	configActionCopyOnSelectClear
)

type configPanelRow struct {
	section     configPanelSection
	label       string
	description string
	keywords    string
	action      configPanelAction
}

var configPanelRows = []configPanelRow{
	{
		section:     configSectionAppearance,
		label:       "Theme",
		description: "Choose the syntax highlighting theme",
		keywords:    "appearance color syntax theme",
		action:      configActionTheme,
	},
	{
		section:     configSectionAppearance,
		label:       "Effects",
		description: "Animate the welcome screen, working indicator, and transitions",
		keywords:    "appearance animation effects motion",
		action:      configActionEffects,
	},
	{
		section:     configSectionAppearance,
		label:       "Tips",
		description: "Show helpful shortcuts and suggestions",
		keywords:    "appearance welcome hints tooltips",
		action:      configActionTips,
	},
	{
		section:     configSectionAppearance,
		label:       "Status line colors",
		description: "Use theme colors for model, branch, and other status details",
		keywords:    "appearance statusline footer colour",
		action:      configActionStatusLineColors,
	},
	{
		section:     configSectionAppearance,
		label:       "Status line",
		description: "Choose which items appear in the status line",
		keywords:    "appearance status line items",
		action:      configActionStatusLine,
	},
	{
		section:     configSectionAppearance,
		label:       "Terminal title",
		description: "Choose which items appear in the terminal title",
		keywords:    "appearance terminal title items",
		action:      configActionTerminalTitle,
	},
	{
		section:     configSectionNotifications,
		label:       "Notifications",
		description: "Let the terminal notify you when a turn finishes or approval is needed",
		keywords:    "notifications alerts bell completion approval",
		action:      configActionNotifications,
	},
	{
		section:     configSectionNotifications,
		label:       "Notify when",
		description: "Notify only while away or while this terminal is focused too",
		keywords:    "notifications notify unfocused always focus",
		action:      configActionNotifyCondition,
	},
	{
		section:     configSectionNotifications,
		label:       "Test notification",
		description: "Send in 5 seconds so you can switch apps before it appears",
		keywords:    "notifications alerts bell sound test",
		action:      configActionNotificationTest,
	},
	{
		section:     configSectionInput,
		label:       "Keyboard",
		description: "Remap TUI shortcuts",
		keywords:    "input keyboard keymap shortcuts",
		action:      configActionKeyboard,
	},
	{
		section:     configSectionInput,
		label:       "Start in Vim mode",
		description: "Start new composers in Vim mode; use /vim for the current composer",
		keywords:    "input editor modal normal vim",
		action:      configActionStartInVimMode,
	},
	{
		section:     configSectionInput,
		label:       "Paste detection",
		description: "Recognize pasted-text bursts and keep multiline input together",
		keywords:    "input burst clipboard multiline paste",
		action:      configActionPasteDetection,
	},
	{
		section:     configSectionSessions,
		label:       "Automatic recaps",
		description: "Prepare recaps while the terminal is unfocused",
		keywords:    "sessions summary background recap",
		action:      configActionAutomaticRecaps,
	},
	{
		section:     configSectionSessions,
		label:       "Session picker layout",
		description: "Choose the session picker row density",
		keywords:    "sessions resume picker comfortable dense layout",
		action:      configActionSessionPicker,
	},
	{
		section:     configSectionSessions,
		label:       "Resume directory",
		description: "Choose the working directory when the current and saved session differ",
		keywords:    "sessions cwd folder path resume ask current",
		action:      configActionResumeCWD,
	},
	{
		section:     configSectionTextSelection,
		label:       "Copy on select",
		description: "Copy transcript, footer, and composer selections on release",
		keywords:    "text selection copy select clipboard auto always never",
		action:      configActionCopyOnSelect,
	},
	{
		section:     configSectionTextSelection,
		label:       "Deselect after copy",
		description: "Clear transcript and footer highlights after automatic copy",
		keywords:    "text selection copy clear highlight deselect",
		action:      configActionCopyOnSelectClear,
	},
	{
		section:     configSectionTextSelection,
		label:       "Right-click paste",
		description: "Paste when nothing is selected; Auto follows platform defaults",
		keywords:    "text selection mouse right click paste",
		action:      configActionRightClickPaste,
	},
}

type configPanelState struct {
	section     configPanelSection
	query       string
	selected    int
	scrollTop   int
	visibleRows int
	effects     bool
	// notice is the panel-local status line (Rust PickerState::notice). It
	// carries save failures and the "Saved · ..." variants without replacing
	// the panel contents.
	notice string
}

type configPanelPendingSave struct {
	keyPath string
	value   any
}

func (m *Model) openConfigPanel() {
	if m == nil {
		return
	}
	m.modal = &modalState{
		id:          "config",
		kind:        ModalKindConfig,
		title:       "Configuration",
		configPanel: &configPanelState{},
	}
	m.notice = ""
}

func (m *Model) configPanelFilteredRows() []configPanelRow {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return nil
	}
	panel := m.modal.configPanel
	if panel.effects {
		return []configPanelRow{
			{
				label:       "Animations",
				description: "Animate the welcome screen, working indicator, and transitions",
				keywords:    "animation effects motion",
				action:      configActionEffects,
			},
			{
				label:       "Composer stars",
				description: "Show stars briefly for new Astra tasks",
				keywords:    "composer stars astra sparkle",
				action:      configActionComposerStars,
			},
		}
	}
	query := strings.ToLower(strings.TrimSpace(panel.query))
	rows := make([]configPanelRow, 0, len(configPanelRows))
	for _, row := range configPanelRows {
		if row.section != panel.section {
			continue
		}
		if query == "" {
			rows = append(rows, row)
			continue
		}
		haystack := strings.ToLower(row.label + " " + row.description + " " + row.keywords + " " + configPanelSectionTitles[row.section])
		if strings.Contains(haystack, query) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m *Model) configPanelValue(row configPanelRow) string {
	if m == nil {
		return ""
	}
	switch row.action {
	case configActionTheme:
		return firstNonEmpty(strings.TrimSpace(m.tuiTheme), "Automatic")
	case configActionEffects:
		if m.animationsEnabled {
			return "On"
		}
		return "Off"
	case configActionComposerStars:
		if !m.animationsEnabled {
			return "Unavailable"
		}
		if m.effects.Starfield {
			return "On"
		}
		return "Off"
	case configActionTips:
		if m.showTooltips {
			return "On"
		}
		return "Off"
	case configActionStatusLineColors:
		if m.statusLineUseColors {
			return "On"
		}
		return "Off"
	case configActionNotifications:
		if m.notificationSettings.CustomSet {
			return "Unavailable"
		}
		if m.notificationSettings.Enabled {
			return "On"
		}
		return "Off"
	case configActionNotificationTest:
		return "Send in 5s"
	case configActionNotifyCondition:
		switch m.notificationCondition {
		case codextui.NotificationConditionAlways:
			return "Always"
		default:
			return "Unfocused"
		}
	case configActionRightClickPaste:
		switch strings.ToLower(strings.TrimSpace(string(m.rightClickPasteMode))) {
		case "on":
			return "On"
		case "off":
			return "Off"
		default:
			return "Auto"
		}
	case configActionPasteDetection:
		if m.disablePasteBurst {
			return "Off"
		}
		return "On"
	case configActionStartInVimMode:
		if m.vimModeDefault {
			return "On"
		}
		return "Off"
	case configActionAutomaticRecaps:
		if m.disableAutoRecap {
			return "Off"
		}
		return "On"
	case configActionSessionPicker:
		if m.sessionPickerDensity == codextui.SessionDensityComfortable {
			return "Comfortable"
		}
		return "Dense"
	case configActionResumeCWD:
		switch normalizeResumeCWD(m.resumeCWD) {
		case "current":
			if m.resumeCWDCurrentUnavailable() {
				return "Current directory · Requires --cd for remote sessions"
			}
			return "Current directory"
		case "session":
			return "Session directory"
		default:
			return "Ask"
		}
	case configActionCopyOnSelect:
		switch normalizeCopyOnSelectMode(m.copyOnSelectMode) {
		case "always":
			return "On"
		case "never":
			return "Off"
		default:
			return "Auto"
		}
	case configActionCopyOnSelectClear:
		if normalizeCopyOnSelectMode(m.copyOnSelectMode) == "never" {
			return "Unavailable"
		}
		if m.copyOnSelectClear {
			return "On"
		}
		return "Off"
	default:
		return "Configure"
	}
}

func (m *Model) updateConfigPanelKey(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return nil
	}
	panel := m.modal.configPanel
	rows := m.configPanelFilteredRows()
	switch message.Type {
	case bubbletea.KeyEsc:
		if panel.effects {
			panel.effects = false
			panel.selected = 0
			panel.scrollTop = 0
			return nil
		}
		m.modal = nil
		m.refreshTranscript()
		return nil
	case bubbletea.KeyCtrlC:
		m.modal = nil
		m.refreshTranscript()
		return nil
	case bubbletea.KeyLeft:
		panel.section = (panel.section + configSectionCount - 1) % configSectionCount
		m.resetConfigPanelSelection()
		return nil
	case bubbletea.KeyRight:
		panel.section = (panel.section + 1) % configSectionCount
		m.resetConfigPanelSelection()
		return nil
	case bubbletea.KeyUp:
		if panel.selected > 0 {
			panel.selected--
		}
		return nil
	case bubbletea.KeyDown:
		if panel.selected+1 < len(rows) {
			panel.selected++
		}
		return nil
	case bubbletea.KeyPgUp:
		panel.selected = max(panel.selected-max(panel.visibleRows, 1), 0)
		return nil
	case bubbletea.KeyPgDown:
		panel.selected = min(panel.selected+max(panel.visibleRows, 1), max(len(rows)-1, 0))
		return nil
	case bubbletea.KeyHome:
		panel.selected = 0
		return nil
	case bubbletea.KeyEnd:
		panel.selected = max(len(rows)-1, 0)
		return nil
	case bubbletea.KeyBackspace:
		if panel.query != "" {
			_, size := utf8.DecodeLastRuneInString(panel.query)
			panel.query = panel.query[:len(panel.query)-size]
			m.resetConfigPanelSelection()
		}
		return nil
	case bubbletea.KeyCtrlU:
		panel.query = ""
		m.resetConfigPanelSelection()
		return nil
	case bubbletea.KeyEnter:
		if panel.selected >= 0 && panel.selected < len(rows) {
			return m.activateConfigRow(rows[panel.selected])
		}
		return nil
	case bubbletea.KeySpace:
		if panel.effects {
			// Rust #53496: the Effects picker uses the shared list toggle
			// handling, so Space toggles the selected row. A configured list
			// binding keeps precedence over that shortcut.
			action := m.configuredListActionForKey("space")
			switch action {
			case "cancel":
				panel.effects = false
				m.resetConfigPanelSelection()
				return nil
			case "", "accept":
				if panel.selected >= 0 && panel.selected < len(rows) {
					return m.activateConfigRow(rows[panel.selected])
				}
			}
			return nil
		}
		panel.query += " "
		m.resetConfigPanelSelection()
		return nil
	case bubbletea.KeyRunes:
		if len(message.Runes) > 0 {
			panel.query += string(message.Runes)
			m.resetConfigPanelSelection()
		}
		return nil
	default:
		return nil
	}
}

// configuredListActionForKey reports the configured list action bound to key.
// Only custom bindings count: unnamed defaults never shadow Space toggling.
func (m *Model) configuredListActionForKey(key string) string {
	if m == nil || m.keymapConfig == nil {
		return ""
	}
	for _, action := range []string{"move_up", "move_down", "move_left", "move_right", "page_up", "page_down", "jump_top", "jump_bottom", "accept", "cancel"} {
		bindings, _, custom := codextui.ResolvedKeymapBindings(m.keymapConfig, "list", action)
		if custom && keymapBindingsContainKey(bindings, key) {
			return action
		}
	}
	return ""
}

func (m *Model) resetConfigPanelSelection() {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return
	}
	m.modal.configPanel.selected = 0
	m.modal.configPanel.scrollTop = 0
}

func (m *Model) activateConfigRow(row configPanelRow) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch row.action {
	case configActionTheme:
		m.modal = nil
		m.openThemePicker()
		return nil
	case configActionStatusLine:
		m.modal = nil
		m.openStatusLineSetup()
		return nil
	case configActionTerminalTitle:
		m.modal = nil
		return m.openTerminalTitleSetup()
	case configActionKeyboard:
		m.modal = nil
		m.openKeymapPicker("", "")
		return nil
	case configActionEffects:
		if m.modal != nil && m.modal.configPanel != nil && !m.modal.configPanel.effects {
			m.modal.configPanel.effects = true
			m.modal.configPanel.selected = 0
			m.modal.configPanel.scrollTop = 0
			return nil
		}
		m.animationsEnabled = !m.animationsEnabled
		return m.saveConfigPanelPreference("tui.animations", m.animationsEnabled)
	case configActionComposerStars:
		if !m.animationsEnabled {
			m.notice = "Turn on Animations to show Astra task stars"
			return nil
		}
		m.effects.Starfield = !m.effects.Starfield
		return m.saveConfigPanelPreference("tui.effects.starfield", m.effects.Starfield)
	case configActionTips:
		m.showTooltips = !m.showTooltips
		return m.saveConfigPanelPreference("tui.show_tooltips", m.showTooltips)
	case configActionStatusLineColors:
		m.statusLineUseColors = !m.statusLineUseColors
		if m.statusControls != nil {
			m.statusControls.StatusLineUseThemeColors = m.statusLineUseColors
		}
		return m.saveConfigPanelPreference("tui.status_line_use_colors", m.statusLineUseColors)
	case configActionNotifications:
		if m.notificationSettings.CustomSet {
			m.notice = "Event-specific notifications are configured in config.toml"
			return nil
		}
		m.notificationSettings.Enabled = !m.notificationSettings.Enabled
		return m.saveConfigPanelPreference("tui.notifications", m.notificationSettings.Enabled)
	case configActionNotificationTest:
		m.notice = "Test notification will appear in 5 seconds."
		return bubbletea.Tick(5*time.Second, func(time.Time) bubbletea.Msg {
			return configNotificationTestMsg{}
		})
	case configActionPasteDetection:
		m.disablePasteBurst = !m.disablePasteBurst
		return m.saveConfigPanelPreference("tui.disable_paste_burst", m.disablePasteBurst)
	case configActionStartInVimMode:
		m.vimModeDefault = !m.vimModeDefault
		return m.saveConfigPanelPreference("tui.vim_mode_default", m.vimModeDefault)
	case configActionAutomaticRecaps:
		m.disableAutoRecap = !m.disableAutoRecap
		return m.saveConfigPanelPreference("tui.auto_recap", !m.disableAutoRecap)
	case configActionNotifyCondition:
		next := codextui.NotificationConditionUnfocused
		if m.notificationCondition != codextui.NotificationConditionAlways {
			next = codextui.NotificationConditionAlways
		}
		m.notificationCondition = next
		return m.saveConfigPanelPreference("tui.notification_condition", string(next))
	case configActionRightClickPaste:
		next := "auto"
		switch strings.ToLower(strings.TrimSpace(string(m.rightClickPasteMode))) {
		case "auto", "":
			next = "on"
		case "on":
			next = "off"
		}
		m.setRightClickPasteMode(next)
		return m.saveConfigPanelPreference("tui.right_click_paste", next)
	case configActionSessionPicker:
		m.sessionPickerDensity = m.sessionPickerDensity.Toggle()
		value := "dense"
		if m.sessionPickerDensity == codextui.SessionDensityComfortable {
			value = "comfortable"
		}
		return m.saveConfigPanelPreference("tui.session_picker_view", value)
	case configActionResumeCWD:
		next := "current"
		switch normalizeResumeCWD(m.resumeCWD) {
		case "ask":
			if m.resumeCWDCurrentUnavailable() {
				next = "session"
			}
		case "current":
			next = "session"
		default:
			next = "ask"
		}
		m.resumeCWD = next
		return m.saveConfigPanelPreference("tui.resume_cwd", next)
	case configActionCopyOnSelect:
		next := "always"
		switch normalizeCopyOnSelectMode(m.copyOnSelectMode) {
		case "auto":
			next = "always"
		case "always":
			next = "never"
		default:
			next = "auto"
		}
		m.copyOnSelectMode = next
		return m.saveConfigPanelPreference("tui.copy_on_select", next)
	case configActionCopyOnSelectClear:
		if normalizeCopyOnSelectMode(m.copyOnSelectMode) == "never" {
			m.notice = "Turn on Copy on select to deselect after copying"
			return nil
		}
		m.copyOnSelectClear = !m.copyOnSelectClear
		return m.saveConfigPanelPreference("tui.copy_on_select_clear_selection", m.copyOnSelectClear)
	default:
		return nil
	}
}

type configNotificationTestMsg struct{}

func (m *Model) postConfigTestNotification() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.notificationPost == nil {
		m.notice = "Notifications are unavailable in this session."
		return nil
	}
	m.notice = "Test notification sent."
	return m.notificationPost("Test notification", m.notificationMethod)
}

func normalizeCopyOnSelectMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "always":
		return "always"
	case "never":
		return "never"
	default:
		return "auto"
	}
}

func normalizeResumeCWD(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "current":
		return "current"
	case "session":
		return "session"
	default:
		return "ask"
	}
}

// resumeCWDCurrentUnavailable reports Rust's ResumeCurrentAvailability for an
// explicit remote workspace without a --cd override.
func (m *Model) resumeCWDCurrentUnavailable() bool {
	return m != nil && m.remoteAppServer && strings.TrimSpace(m.remoteCWDOverride) == ""
}

func (m *Model) saveConfigPanelPreference(keyPath string, value any) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.onWriteSettings == nil {
		m.notice = "Configuration updated for this session."
		m.refreshTranscript()
		return nil
	}
	m.pendingConfigPanelSave = &configPanelPendingSave{keyPath: strings.TrimSpace(keyPath), value: value}
	cmd := m.writeSettings(settingsWriteKindConfig, []SettingsEdit{{KeyPath: keyPath, Value: value}})
	if cmd == nil {
		m.pendingConfigPanelSave = nil
	}
	return cmd
}

// setConfigPanelNotice updates the open configuration panel's status line.
func (m *Model) setConfigPanelNotice(notice string) bool {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return false
	}
	m.modal.configPanel.notice = strings.TrimSpace(notice)
	return true
}

// finishConfigPanelSave reports Rust's save-notice variants: a preference that
// the reloaded config did not retain was overridden, while Vim mode only
// affects composers created after the save.
func (m *Model) finishConfigPanelSave(result SettingsWriteResult) (string, bool) {
	if m == nil {
		return "", false
	}
	pending := m.pendingConfigPanelSave
	m.pendingConfigPanelSave = nil
	if pending == nil {
		return "", false
	}
	notice := ""
	if effective, ok := configPanelEffectiveValue(result, pending.keyPath); ok && !reflect.DeepEqual(effective, pending.value) {
		notice = "Saved · overridden by config or system setting"
	} else if pending.keyPath == "tui.vim_mode_default" {
		notice = "Saved · applies to new composers"
	}
	return notice, true
}

// configPanelEffectiveValue reads the resolved value of a config-panel
// preference from a settings reload. A nil/absent field is unknown, not a
// mismatch, because partial results preserve the live value.
func configPanelEffectiveValue(result SettingsWriteResult, keyPath string) (any, bool) {
	switch keyPath {
	case "tui.animations":
		if result.AnimationsEnabled != nil {
			return *result.AnimationsEnabled, true
		}
	case "tui.effects.starfield":
		if result.Effects != nil {
			return result.Effects.Starfield, true
		}
	case "tui.show_tooltips":
		if result.ShowTooltips != nil {
			return *result.ShowTooltips, true
		}
	case "tui.status_line_use_colors":
		if result.StatusLineUseColors != nil {
			return *result.StatusLineUseColors, true
		}
	case "tui.notifications":
		if result.Notifications != nil {
			return result.Notifications.Enabled, true
		}
	case "tui.auto_recap":
		if result.AutoRecap != nil {
			return *result.AutoRecap, true
		}
	case "tui.vim_mode_default":
		if result.StartInVimMode != nil {
			return *result.StartInVimMode, true
		}
	case "tui.notification_condition":
		if value := strings.TrimSpace(string(result.NotificationCondition)); value != "" {
			return value, true
		}
	case "tui.right_click_paste":
		if result.RightClickPaste != nil {
			return strings.TrimSpace(*result.RightClickPaste), true
		}
	case "tui.session_picker_view":
		if value := strings.TrimSpace(result.SessionPickerView); value != "" {
			return value, true
		}
	case "tui.resume_cwd":
		if result.ResumeCWD != nil {
			return normalizeResumeCWD(*result.ResumeCWD), true
		}
	case "tui.copy_on_select":
		if result.CopyOnSelect != nil {
			return normalizeCopyOnSelectMode(*result.CopyOnSelect), true
		}
	case "tui.copy_on_select_clear_selection":
		if result.CopyOnSelectClearSelection != nil {
			return *result.CopyOnSelectClearSelection, true
		}
	}
	return nil, false
}

func (m *Model) renderConfigPanel() string {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return ""
	}
	panel := m.modal.configPanel
	width := max(m.width-4, 1)
	rows := m.configPanelFilteredRows()
	if len(rows) == 0 {
		panel.selected = 0
	} else {
		panel.selected = clampModalInt(panel.selected, 0, len(rows)-1)
	}
	activeStyle := func(text string) string {
		return lipgloss.NewStyle().Bold(true).Reverse(true).Render(text)
	}
	inactiveStyle := func(text string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(text)
	}
	maxRows := max(m.height-6, 8)
	optionBudget := max(maxRows-6, 1)
	title := m.modal.title
	var header []string
	footer := "type to search \u00b7 \u2190/\u2192 tabs \u00b7 enter open \u00b7 esc close"
	if panel.effects {
		title = "Effects"
		header = []string{title, "Configure visual effects", ""}
		optionBudget = max(maxRows-4, 1)
		footer = "enter toggle \u00b7 esc back"
	} else {
		tabs := bottompane.FilledTabBarStyledText(configPanelSectionTitles, int(panel.section), width, activeStyle, inactiveStyle)
		subtitle := strings.TrimSpace(panel.notice)
		if subtitle == "" {
			subtitle = "Changes save automatically"
		}
		subtitle = codextui.TruncateWithEllipsis(subtitle, width)
		query := strings.TrimSpace(panel.query)
		searchLine := "Search: " + query
		if query == "" {
			searchLine = "Type to search"
		}
		searchLine = codextui.TruncateWithEllipsis(searchLine, width)
		header = []string{title, inactiveStyle(subtitle), tabs, searchLine, ""}
	}
	footer = codextui.TruncateWithEllipsis(footer, width)
	start, visible, above, below := modalOptionWindow(len(rows), panel.selected, panel.scrollTop, optionBudget)
	panel.scrollTop = start
	panel.visibleRows = visible

	labelWidth := 0
	for _, row := range rows {
		labelWidth = max(labelWidth, codextui.DisplayWidth(row.label))
	}
	labelWidth = min(labelWidth, 24)
	lines := append([]string(nil), header...)
	if above {
		lines = append(lines, "\u2191")
	}
	for index := start; index < start+visible && index < len(rows); index++ {
		row := rows[index]
		line := "  " + row.label
		if padding := labelWidth - codextui.DisplayWidth(row.label) + 2; padding > 0 {
			line += strings.Repeat(" ", padding)
		}
		line += m.configPanelValue(row)
		if row.description != "" && width > labelWidth+16 {
			line += "  \u2014 " + row.description
		}
		line = codextui.TruncateWithEllipsis(line, width)
		if index == panel.selected {
			line = codextui.RenderSelectedRowFilled("\u203a "+strings.TrimPrefix(line, "  "), width)
		}
		lines = append(lines, line)
	}
	if len(rows) == 0 {
		lines = append(lines, "No matching settings")
	}
	if below {
		lines = append(lines, "\u2193")
	}
	lines = append(lines, "", inactiveStyle(footer))
	return strings.Join(lines, "\n")
}
