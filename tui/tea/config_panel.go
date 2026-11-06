package tea

import (
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	bubbletea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/tui/keymsg"
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

// configPanelInlineDescriptionMinWidth mirrors Rust #53429's
// SelectionDescriptionLayout::SelectedBelowWhenNarrow min_description_width.
const configPanelInlineDescriptionMinWidth = 45

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
	configActionModelProviders
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

// configPanelRows mirrors Rust config_panel::mod::settings(): the label,
// description and search keywords are the user-visible contract shared by the
// group tabs and the global search results.
var configPanelRows = []configPanelRow{
	{
		section:     configSectionAppearance,
		label:       "Theme",
		description: "Choose a syntax color palette with a live preview",
		keywords:    "colors dark light palette",
		action:      configActionTheme,
	},
	{
		section:     configSectionAppearance,
		label:       "Effects",
		description: "Configure animations and composer effects",
		keywords:    "animations motion accessibility spinner starfield stars composer stars",
		action:      configActionEffects,
	},
	{
		section:     configSectionAppearance,
		label:       "Tips",
		description: "Show helpful shortcuts and suggestions",
		keywords:    "welcome hints tooltips",
		action:      configActionTips,
	},
	{
		section:     configSectionAppearance,
		label:       "Status line colors",
		description: "Use theme colors for the model, branch, and other status line details",
		keywords:    "statusline footer colour",
		action:      configActionStatusLineColors,
	},
	{
		section:     configSectionAppearance,
		label:       "Status line display",
		description: "Choose and reorder the information below the composer",
		keywords:    "statusline footer model branch context",
		action:      configActionStatusLine,
	},
	{
		section:     configSectionAppearance,
		label:       "Terminal title display",
		description: "Choose and reorder the information in your terminal tab or window title",
		keywords:    "title tab window name",
		action:      configActionTerminalTitle,
	},
	{
		section:     configSectionNotifications,
		label:       "Notifications",
		description: "Let your terminal notify you when a turn finishes or approval is needed",
		keywords:    "alerts bell completion approval",
		action:      configActionNotifications,
	},
	{
		section:     configSectionNotifications,
		label:       "Notify when",
		description: "Notify only while you are away or while this terminal is focused too",
		keywords:    "focus alerts background always unfocused",
		action:      configActionNotifyCondition,
	},
	{
		section:     configSectionNotifications,
		label:       "Test notification",
		description: "Send in 5 seconds so you can switch apps before the notification appears",
		keywords:    "alerts bell sound test",
		action:      configActionNotificationTest,
	},
	{
		section:     configSectionInput,
		label:       "Keyboard shortcuts",
		description: "Browse actions and remap their keys",
		keywords:    "keymap bindings hotkeys",
		action:      configActionKeyboard,
	},
	{
		section:     configSectionInput,
		label:       "Start in Vim mode",
		description: "Start new composers in Vim mode and use /vim for the current composer",
		keywords:    "editor modal normal",
		action:      configActionStartInVimMode,
	},
	{
		section:     configSectionInput,
		label:       "Paste detection",
		description: "Recognize pasted-text bursts and keep multiline input together",
		keywords:    "burst clipboard multiline",
		action:      configActionPasteDetection,
	},
	{
		section:     configSectionSessions,
		label:       "Automatic recaps",
		description: "Prepare recaps while the terminal is unfocused",
		keywords:    "summary background",
		action:      configActionAutomaticRecaps,
	},
	{
		section:     configSectionSessions,
		label:       "Session picker layout",
		description: "Choose tighter rows or a roomier list when resuming and forking conversations",
		keywords:    "dense comfortable spacing resume fork",
		action:      configActionSessionPicker,
	},
	{
		section:     configSectionSessions,
		label:       "Resume directory",
		description: "Choose which working directory to use when the current directory and saved session differ",
		keywords:    "cwd folder path fork ask",
		action:      configActionResumeCWD,
	},
	{
		section:     configSectionSessions,
		label:       "Model providers",
		description: "Browse providers configured on the connected app server",
		keywords:    "provider gateway endpoint model default",
		action:      configActionModelProviders,
	},
	{
		section:     configSectionTextSelection,
		label:       "Copy on select",
		description: "Copy transcript, footer, and composer selections on release; Auto follows terminal defaults",
		keywords:    "clipboard mouse selection automatic drag",
		action:      configActionCopyOnSelect,
	},
	{
		section:     configSectionTextSelection,
		label:       "Deselect after copy",
		description: "Remove read-only transcript and footer highlights after automatic copy succeeds",
		keywords:    "clipboard deselect clear highlight automatic mouse selection",
		action:      configActionCopyOnSelectClear,
	},
	{
		section:     configSectionTextSelection,
		label:       "Right-click paste",
		description: "Paste when nothing is selected, with Auto following platform defaults and On also enabling macOS",
		keywords:    "clipboard mouse paste automatic",
		action:      configActionRightClickPaste,
	},
}

type configPanelState struct {
	section configPanelSection
	query   string
	// search mirrors Rust's SearchMode::OnDemand search: the search field only
	// appears after the user presses "/" inside the panel.
	search bool
	// choice is the child picker an editor row opens (Rust pushes a
	// SelectionViewParams::picker() view onto the view stack).
	choice      *configPanelChoice
	selected    int
	scrollTop   int
	visibleRows int
	effects     bool
	// notice is the panel-local status line (Rust PickerState::notice). It
	// carries save failures and the "Saved · ..." variants without replacing
	// the panel contents.
	notice string
	// providers is the open model-provider browser, if any.
	providers *configProviderBrowserState
}

// configPanelChoice mirrors Rust's `SelectionViewParams::picker()` child view.
type configPanelChoice struct {
	action   configPanelAction
	title    string
	subtitle string
	selected int
	items    []configPanelChoiceItem
}

type configPanelChoiceItem struct {
	label          string
	value          string
	current        bool
	disabledReason string
}

type configPanelPendingSave struct {
	keyPath string
	value   any
}

func (m *Model) openConfigPanel() {
	if m == nil {
		return
	}
	m.replaceModal(&modalState{
		id:          "config",
		kind:        ModalKindConfig,
		title:       "Configuration",
		configPanel: &configPanelState{},
	})
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
		if query == "" {
			if row.section != panel.section {
				continue
			}
			rows = append(rows, row)
			continue
		}
		// Rust #53429: search spans every group, and each result keeps its
		// group tag so a match outside the active tab is still identifiable.
		haystack := strings.ToLower(configPanelSectionTitles[row.section] + " " + row.label + " " + m.configPanelValue(row) + " " + row.keywords + " " + row.description)
		if strings.Contains(haystack, query) {
			rows = append(rows, row)
		}
	}
	return rows
}

// configPanelToggleValue mirrors Rust's ConfigSettingValue::Toggle label.
func configPanelToggleValue(on bool) string {
	if on {
		return "[x] On"
	}
	return "[ ] Off"
}

// rightClickPastePlatformDefault mirrors Rust PasteEnvironment::detect's
// PlatformDefault reading used by the Auto right-click-paste label.
func rightClickPastePlatformDefault() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "linux"
}

func (m *Model) configPanelValue(row configPanelRow) string {
	if m == nil {
		return ""
	}
	// The Effects child picker renders the same rows as toggles.
	if m.modal != nil && m.modal.configPanel != nil && m.modal.configPanel.effects {
		switch row.action {
		case configActionEffects:
			return configPanelToggleValue(m.animationsEnabled)
		case configActionComposerStars:
			if !m.animationsEnabled {
				return "Unavailable"
			}
			return configPanelToggleValue(m.effects.Starfield)
		}
	}
	switch row.action {
	case configActionTheme:
		return firstNonEmpty(strings.TrimSpace(m.tuiTheme), "Automatic") + " \u203a"
	case configActionEffects, configActionStatusLine, configActionTerminalTitle, configActionKeyboard:
		return "Configure \u203a"
	case configActionTips:
		return configPanelToggleValue(m.showTooltips)
	case configActionStatusLineColors:
		return configPanelToggleValue(m.statusLineUseColors)
	case configActionComposerStars:
		if !m.animationsEnabled {
			return "Unavailable"
		}
		return configPanelToggleValue(m.effects.Starfield)
	case configActionNotifications:
		if m.notificationSettings.CustomSet {
			return "Unavailable"
		}
		return configPanelToggleValue(m.notificationSettings.Enabled)
	case configActionNotifyCondition:
		if !m.notificationSettings.Enabled {
			return "Unavailable"
		}
		if m.notificationCondition == codextui.NotificationConditionAlways {
			return "Always \u203a"
		}
		return "Unfocused \u203a"
	case configActionNotificationTest:
		return "Send in 5s"
	case configActionStartInVimMode:
		return configPanelToggleValue(m.vimModeDefault)
	case configActionPasteDetection:
		return configPanelToggleValue(!m.disablePasteBurst)
	case configActionAutomaticRecaps:
		return configPanelToggleValue(!m.disableAutoRecap)
	case configActionSessionPicker:
		if m.sessionPickerDensity == codextui.SessionDensityComfortable {
			return "Comfortable \u203a"
		}
		return "Dense \u203a"
	case configActionModelProviders:
		return "Browse \u203a"
	case configActionResumeCWD:
		switch normalizeResumeCWD(m.resumeCWD) {
		case "current":
			return "Current directory \u203a"
		case "session":
			return "Session directory \u203a"
		default:
			return "Ask \u203a"
		}
	case configActionCopyOnSelect:
		switch normalizeCopyOnSelectMode(m.copyOnSelectMode) {
		case "always":
			return "On \u203a"
		case "never":
			return "Off \u203a"
		default:
			return "Auto \u00b7 " + configPanelOnOff(m.copyOnSelectEnabled()) + " \u203a"
		}
	case configActionCopyOnSelectClear:
		if !m.copyOnSelectEnabled() {
			return "Unavailable"
		}
		return configPanelToggleValue(m.copyOnSelectClear)
	case configActionRightClickPaste:
		switch tuiapp.ParseRightClickPasteMode(string(m.rightClickPasteMode)) {
		case tuiapp.RightClickPasteOn:
			return "On \u203a"
		case tuiapp.RightClickPasteOff:
			return "Off \u203a"
		default:
			return "Auto \u00b7 " + configPanelOnOff(rightClickPastePlatformDefault()) + " \u203a"
		}
	default:
		return "Configure \u203a"
	}
}

func configPanelOnOff(on bool) string {
	if on {
		return "On"
	}
	return "Off"
}

// configPanelRowDisabledReason mirrors Rust's ConfigSettingValue::Unavailable and
// DependentToggle{dependency_met: false} rows: the reason replaces the
// description and the row cannot be activated.
func (m *Model) configPanelRowDisabledReason(row configPanelRow) string {
	if m == nil {
		return ""
	}
	switch row.action {
	case configActionNotifications:
		if m.notificationSettings.CustomSet {
			return "Event-specific notifications are configured in config.toml"
		}
	case configActionNotifyCondition:
		if !m.notificationSettings.Enabled {
			return "Turn on notifications to choose when they appear"
		}
	case configActionCopyOnSelectClear:
		if !m.copyOnSelectEnabled() {
			return "Turn on Copy on select to deselect after copying"
		}
	}
	return ""
}

// configPanelOptionDisabledReason resolves the disabled reason for the current
// picker surface (the main catalog or the Effects child picker).
func (m *Model) configPanelOptionDisabledReason(row configPanelRow) string {
	if reason := m.configPanelRowDisabledReason(row); reason != "" {
		return reason
	}
	if m != nil && m.modal != nil && m.modal.configPanel != nil && m.modal.configPanel.effects {
		return m.configEffectsRowDisabledReason(row)
	}
	return ""
}

// configEffectsRowDisabledReason mirrors the Effects picker's disabled row for
// Composer stars while animations are off.
func (m *Model) configEffectsRowDisabledReason(row configPanelRow) string {
	if m == nil {
		return ""
	}
	if row.action == configActionComposerStars && !m.animationsEnabled {
		return "Turn on Animations to show Astra task stars"
	}
	return ""
}

func (m *Model) updateConfigPanelKey(message keymsg.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return nil
	}
	panel := m.modal.configPanel
	if panel.providers != nil {
		return m.updateConfigProviderBrowserKey(message)
	}
	if panel.choice != nil {
		return m.updateConfigPanelChoiceKey(message)
	}
	rows := m.configPanelFilteredRows()
	switch message.Type {
	case keymsg.KeyEsc:
		if panel.effects {
			panel.effects = false
			panel.selected = 0
			panel.scrollTop = 0
			return nil
		}
		// Rust SearchMode::OnDemand: Esc leaves the "/" search and restores the
		// active tab before it closes the panel.
		if panel.search {
			panel.search = false
			panel.query = ""
			m.resetConfigPanelSelection()
			return nil
		}
		m.modal = nil
		m.refreshTranscript()
		return nil
	case keymsg.KeyCtrlC:
		m.modal = nil
		m.refreshTranscript()
		return nil
	case keymsg.KeyLeft:
		// Rust switches the underlying tab during a "/" search but keeps the
		// query and its results visible until Esc.
		panel.section = (panel.section + configSectionCount - 1) % configSectionCount
		if !panel.search {
			m.resetConfigPanelSelection()
		}
		return nil
	case keymsg.KeyRight:
		panel.section = (panel.section + 1) % configSectionCount
		if !panel.search {
			m.resetConfigPanelSelection()
		}
		return nil
	case keymsg.KeyUp:
		m.configPanelMoveSelection(-1)
		return nil
	case keymsg.KeyDown:
		m.configPanelMoveSelection(1)
		return nil
	case keymsg.KeyPgUp:
		panel.selected = max(panel.selected-max(panel.visibleRows, 1), 0)
		return nil
	case keymsg.KeyPgDown:
		panel.selected = min(panel.selected+max(panel.visibleRows, 1), max(len(rows)-1, 0))
		return nil
	case keymsg.KeyHome:
		panel.selected = 0
		return nil
	case keymsg.KeyEnd:
		panel.selected = max(len(rows)-1, 0)
		return nil
	case keymsg.KeyBackspace:
		if panel.search && panel.query != "" {
			_, size := utf8.DecodeLastRuneInString(panel.query)
			panel.query = panel.query[:len(panel.query)-size]
			m.resetConfigPanelSelection()
		}
		return nil
	case keymsg.KeyCtrlU:
		if panel.search {
			panel.query = ""
			m.resetConfigPanelSelection()
		}
		return nil
	case keymsg.KeyEnter:
		if panel.selected >= 0 && panel.selected < len(rows) {
			return m.activateConfigRow(rows[panel.selected])
		}
		return nil
	case keymsg.KeySpace:
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
		if panel.search {
			panel.query += " "
			m.resetConfigPanelSelection()
		}
		return nil
	case keymsg.KeyRunes:
		if len(message.Runes) == 0 {
			return nil
		}
		// An active search reserves printable keys for the query (Rust #53429
		// list_selection_view::handle_key_event).
		if panel.search {
			panel.query += string(message.Runes)
			m.resetConfigPanelSelection()
			return nil
		}
		if m.applyConfigPanelListAction(message) {
			return nil
		}
		// Rust's config picker uses SearchMode::OnDemand("/"): typing does not
		// start a search, and plain letters stay out of the query until the
		// search field is open. The hint only exists while "/" is unbound on the
		// list surface.
		if len(message.Runes) == 1 && message.Runes[0] == '/' && m.configPanelSearchKeyAvailable() {
			panel.search = true
			panel.query = ""
			m.resetConfigPanelSelection()
		}
		return nil
	default:
		return nil
	}
}

// configPanelListActionForSpec resolves the list-surface action bound to a key
// spec, mirroring Rust's ListKeymap lookups inside the config picker.
func (m *Model) configPanelListActionForSpec(keySpec string) string {
	for _, action := range []string{
		"move_left", "move_right", "move_up", "move_down",
		"page_up", "page_down", "jump_top", "jump_bottom", "cancel",
	} {
		if m.keyMatches("list", action, keySpec) {
			return action
		}
	}
	return ""
}

// configPanelSearchKeyAvailable mirrors Rust's search_hint gate: "/" opens the
// search only when no list binding claims it.
func (m *Model) configPanelSearchKeyAvailable() bool {
	for _, action := range []string{
		"move_left", "move_right", "move_up", "move_down",
		"page_up", "page_down", "jump_top", "jump_bottom", "accept", "cancel",
	} {
		if m.keyMatches("list", action, "/") {
			return false
		}
	}
	return true
}

// applyConfigPanelListAction runs the list binding a printable key resolves to
// and reports whether it was consumed.
func (m *Model) applyConfigPanelListAction(message keymsg.KeyMsg) bool {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return false
	}
	panel := m.modal.configPanel
	switch m.configPanelListActionForSpec(keySpecFromKeyMsg(message)) {
	case "move_up":
		m.configPanelMoveSelection(-1)
	case "move_down":
		m.configPanelMoveSelection(1)
	case "move_left":
		if panel.effects {
			return false
		}
		panel.section = (panel.section + configSectionCount - 1) % configSectionCount
		m.resetConfigPanelSelection()
	case "move_right":
		if panel.effects {
			return false
		}
		panel.section = (panel.section + 1) % configSectionCount
		m.resetConfigPanelSelection()
	case "page_up":
		rows := m.configPanelFilteredRows()
		panel.selected = max(panel.selected-max(panel.visibleRows, 1), 0)
		if len(rows) == 0 {
			panel.selected = 0
		}
	case "page_down":
		rows := m.configPanelFilteredRows()
		panel.selected = min(panel.selected+max(panel.visibleRows, 1), max(len(rows)-1, 0))
	case "jump_top":
		panel.selected = 0
		panel.scrollTop = 0
	case "jump_bottom":
		panel.selected = max(len(m.configPanelFilteredRows())-1, 0)
	case "cancel":
		m.modal = nil
		m.refreshTranscript()
	default:
		return false
	}
	return true
}

// configPanelFirstEnabledRow mirrors Rust's first_enabled_visible_idx fallback.
func (m *Model) configPanelFirstEnabledRow(rows []configPanelRow) int {
	for index, row := range rows {
		if m.configPanelOptionDisabledReason(row) == "" {
			return index
		}
	}
	return 0
}

// configPanelMoveSelection mirrors Rust's wrapping move_up/move_down, which skip
// disabled rows.
func (m *Model) configPanelMoveSelection(delta int) {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return
	}
	panel := m.modal.configPanel
	rows := m.configPanelFilteredRows()
	if len(rows) == 0 {
		panel.selected = 0
		return
	}
	index := clampModalInt(panel.selected, 0, len(rows)-1)
	for step := 0; step < len(rows); step++ {
		index += delta
		if index < 0 {
			index = len(rows) - 1
		} else if index >= len(rows) {
			index = 0
		}
		if m.configPanelOptionDisabledReason(rows[index]) == "" {
			break
		}
	}
	panel.selected = index
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
	case configActionModelProviders:
		// Rust opens the browsing picker instead of changing the provider.
		return m.openConfigProviderBrowser()
	case configActionNotifyCondition, configActionRightClickPaste, configActionSessionPicker,
		configActionResumeCWD, configActionCopyOnSelect:
		// Rust opens a child picker for these editors instead of cycling the
		// value in place (config_panel/render.rs::notification_condition,
		// right_click_paste, session_picker_layout, resume_directory,
		// copy_on_select).
		m.openConfigPanelChoice(row)
		return nil
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

// openConfigPanelChoice opens the child picker Rust pushes for an editor row.
func (m *Model) openConfigPanelChoice(row configPanelRow) {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return
	}
	choice := &configPanelChoice{action: row.action, title: row.label, subtitle: row.description}
	add := func(label, value string, current bool) int {
		choice.items = append(choice.items, configPanelChoiceItem{label: label, value: value, current: current})
		if current {
			choice.selected = len(choice.items) - 1
		}
		return len(choice.items) - 1
	}
	switch row.action {
	case configActionNotifyCondition:
		current := m.notificationCondition
		add("Unfocused", string(codextui.NotificationConditionUnfocused), current != codextui.NotificationConditionAlways)
		add("Always", string(codextui.NotificationConditionAlways), current == codextui.NotificationConditionAlways)
	case configActionSessionPicker:
		comfortable := m.sessionPickerDensity == codextui.SessionDensityComfortable
		add("Dense", "dense", !comfortable)
		add("Comfortable", "comfortable", comfortable)
	case configActionResumeCWD:
		choice.subtitle = "Choose the working directory when resuming, with --cd taking precedence for this session"
		current := normalizeResumeCWD(m.resumeCWD)
		add("Ask", "ask", current == "ask")
		currentIndex := add("Current directory", "current", current == "current")
		if m.resumeCWDCurrentUnavailable() {
			choice.items[currentIndex].disabledReason = "Requires --cd for remote sessions"
		}
		add("Session directory", "session", current == "session")
	case configActionCopyOnSelect:
		current := normalizeCopyOnSelectMode(m.copyOnSelectMode)
		add("Auto (default)", "auto", current == "auto")
		add("On", "always", current == "always")
		add("Off", "never", current == "never")
	case configActionRightClickPaste:
		current := string(tuiapp.ParseRightClickPasteMode(string(m.rightClickPasteMode)))
		add("Auto (default)", "auto", current == "auto")
		add("On", "on", current == "on")
		add("Off", "off", current == "off")
	default:
		return
	}
	// Rust's SelectionViewParams::picker() starts on initial_selected_idx (the
	// current value) and only falls back to the first enabled row when that
	// row is disabled.
	if choice.selected >= 0 && choice.selected < len(choice.items) && choice.items[choice.selected].disabledReason != "" {
		for index, item := range choice.items {
			if item.disabledReason == "" {
				choice.selected = index
				break
			}
		}
	}
	m.modal.configPanel.choice = choice
}

// applyConfigPanelChoiceItem saves the value picked in a child picker.
func (m *Model) applyConfigPanelChoiceItem(action configPanelAction, item configPanelChoiceItem) bubbletea.Cmd {
	switch action {
	case configActionNotifyCondition:
		m.notificationCondition = codextui.NotificationCondition(item.value)
		return m.saveConfigPanelPreference("tui.notification_condition", item.value)
	case configActionSessionPicker:
		if item.value == "comfortable" {
			m.sessionPickerDensity = codextui.SessionDensityComfortable
		} else {
			m.sessionPickerDensity = codextui.SessionDensityDense
		}
		return m.saveConfigPanelPreference("tui.session_picker_view", item.value)
	case configActionResumeCWD:
		m.resumeCWD = item.value
		return m.saveConfigPanelPreference("tui.resume_cwd", item.value)
	case configActionCopyOnSelect:
		m.copyOnSelectMode = item.value
		return m.saveConfigPanelPreference("tui.copy_on_select", item.value)
	case configActionRightClickPaste:
		m.setRightClickPasteMode(item.value)
		return m.saveConfigPanelPreference("tui.right_click_paste", item.value)
	}
	return nil
}

func (m *Model) moveConfigPanelChoice(delta int) {
	if m == nil || m.modal == nil || m.modal.configPanel == nil || m.modal.configPanel.choice == nil {
		return
	}
	choice := m.modal.configPanel.choice
	total := len(choice.items)
	if total == 0 {
		return
	}
	index := clampModalInt(choice.selected, 0, total-1)
	for step := 0; step < total; step++ {
		index += delta
		if index < 0 {
			index = total - 1
		} else if index >= total {
			index = 0
		}
		if choice.items[index].disabledReason == "" {
			break
		}
	}
	choice.selected = index
}

func (m *Model) jumpConfigPanelChoice(number int) {
	if m == nil || m.modal == nil || m.modal.configPanel == nil || m.modal.configPanel.choice == nil {
		return
	}
	choice := m.modal.configPanel.choice
	count := 0
	for index, item := range choice.items {
		if item.disabledReason != "" {
			continue
		}
		count++
		if count == number {
			choice.selected = index
			return
		}
	}
}

func (m *Model) updateConfigPanelChoiceKey(message keymsg.KeyMsg) bubbletea.Cmd {
	panel := m.modal.configPanel
	choice := panel.choice
	switch message.Type {
	case keymsg.KeyEsc:
		panel.choice = nil
		return nil
	case keymsg.KeyCtrlC:
		m.modal = nil
		m.refreshTranscript()
		return nil
	case keymsg.KeyUp:
		m.moveConfigPanelChoice(-1)
		return nil
	case keymsg.KeyDown:
		m.moveConfigPanelChoice(1)
		return nil
	case keymsg.KeyHome:
		choice.selected = 0
		return nil
	case keymsg.KeyEnd:
		choice.selected = max(len(choice.items)-1, 0)
		return nil
	case keymsg.KeyEnter:
		if choice.selected < 0 || choice.selected >= len(choice.items) {
			return nil
		}
		item := choice.items[choice.selected]
		if item.disabledReason != "" {
			return nil
		}
		panel.choice = nil
		return m.applyConfigPanelChoiceItem(choice.action, item)
	case keymsg.KeyRunes:
		if len(message.Runes) == 1 && message.Runes[0] >= '1' && message.Runes[0] <= '9' {
			m.jumpConfigPanelChoice(int(message.Runes[0] - '0'))
			return nil
		}
		switch m.configPanelListActionForSpec(keySpecFromKeyMsg(message)) {
		case "move_up":
			m.moveConfigPanelChoice(-1)
		case "move_down":
			m.moveConfigPanelChoice(1)
		case "cancel":
			panel.choice = nil
		}
		return nil
	default:
		return nil
	}
}

// renderConfigPanelChoice mirrors Rust's SelectionViewParams::picker() child
// view: title, subtitle, the numbered rows with their (current)/(disabled)
// markers and the picker footer.
func (m *Model) renderConfigPanelChoice(choice *configPanelChoice, rowWidth int) string {
	contentWidth := max(rowWidth-2, 1)
	lines := []string{
		"  " + choice.title,
		"  " + codextui.TruncateWithEllipsis(choice.subtitle, contentWidth),
		"",
		"",
	}
	enabled := 0
	for _, item := range choice.items {
		if item.disabledReason == "" {
			enabled++
		}
	}
	numberWidth := max(len(strconv.Itoa(max(enabled, 1))), 1)
	prefixes := make([]string, len(choice.items))
	names := make([]string, len(choice.items))
	maxNameWidth := 0
	number := 0
	for index, item := range choice.items {
		marker := "  "
		if index == choice.selected {
			marker = "\u203a "
		}
		gutter := strings.Repeat(" ", numberWidth+2)
		if item.disabledReason == "" {
			number++
			gutter = strconv.Itoa(number) + ". "
			if pad := numberWidth + 2 - codextui.DisplayWidth(gutter); pad > 0 {
				gutter += strings.Repeat(" ", pad)
			}
		}
		prefixes[index] = marker + gutter
		names[index] = item.label
		if item.current {
			names[index] += " (current)"
		}
		if item.disabledReason != "" {
			names[index] += " (disabled)"
		}
		maxNameWidth = max(maxNameWidth, codextui.DisplayWidth(prefixes[index]+names[index]))
	}
	descCol := configPanelClampDescCol(maxNameWidth+2, rowWidth)
	for index, item := range choice.items {
		line := prefixes[index] + names[index]
		if gap := descCol - codextui.DisplayWidth(line); gap > 0 && item.disabledReason != "" {
			line += strings.Repeat(" ", gap)
		}
		if item.disabledReason != "" {
			line += item.disabledReason
		}
		line = codextui.TruncateWithEllipsis(line, rowWidth)
		if index == choice.selected {
			line = codextui.RenderSelectedRowFilled(line, rowWidth)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", "  enter select \u00b7 esc back")
	return strings.Join(lines, "\n")
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

// configPanelMaxSectionRows mirrors Rust's max_visible_rows: the largest group
// height, which also reserves the search result viewport.
func configPanelMaxSectionRows() int {
	counts := make([]int, configSectionCount)
	for _, row := range configPanelRows {
		counts[row.section]++
	}
	largest := 0
	for _, count := range counts {
		largest = max(largest, count)
	}
	return largest
}

// configPanelDescriptionColumn mirrors Rust's compute_desc_col for the config
// picker: the name column is the widest label (AutoAllRows) narrowed by the
// explicit name_column_width, plus the selection prefix and a two-cell gap.
func configPanelDescriptionColumn(rows []configPanelRow, width int) int {
	if width <= 1 {
		return 0
	}
	nameColumnWidth := 0
	for _, row := range configPanelRows {
		nameColumnWidth = max(nameColumnWidth, codextui.DisplayWidth(row.label))
	}
	maxNameWidth := 0
	for _, row := range rows {
		maxNameWidth = max(maxNameWidth, 2+codextui.DisplayWidth(row.label))
	}
	return configPanelClampDescCol(max(nameColumnWidth, maxNameWidth)+2, width)
}

func configPanelClampDescCol(descCol, width int) int {
	maxDescCol := width - 1
	if autoCap := max(width*70/100, 1); maxDescCol > autoCap {
		maxDescCol = autoCap
	}
	if descCol > maxDescCol {
		descCol = maxDescCol
	}
	if descCol < 0 {
		descCol = 0
	}
	return descCol
}

// configPanelRowLine builds Rust's build_full_line output: selection prefix,
// name padded to desc_col, the 20-cell category tag, then the description.
func configPanelRowLine(label, value, description string, descCol int, selected, narrow bool) string {
	name := label
	if nameLimit := descCol - 4; nameLimit > 0 && codextui.DisplayWidth(name) > nameLimit {
		name = codextui.TruncateWithEllipsis(name, nameLimit)
	}
	var builder strings.Builder
	if selected {
		builder.WriteString("\u203a ")
	} else {
		builder.WriteString("  ")
	}
	builder.WriteString(name)
	if gap := descCol - 2 - codextui.DisplayWidth(name); gap > 0 {
		builder.WriteString(strings.Repeat(" ", gap))
	}
	tag := value
	if tagWidth := codextui.DisplayWidth(tag); tagWidth < 20 {
		tag += strings.Repeat(" ", 20-tagWidth)
	}
	builder.WriteString(tag)
	if description != "" && !narrow {
		builder.WriteString(" ")
		builder.WriteString(description)
	}
	return builder.String()
}

func (m *Model) renderConfigPanel() string {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return ""
	}
	panel := m.modal.configPanel
	rowWidth := max(m.width, 1)
	if panel.providers != nil {
		return m.renderConfigProviderBrowser(panel.providers, rowWidth)
	}
	if panel.choice != nil {
		return m.renderConfigPanelChoice(panel.choice, rowWidth)
	}
	rows := m.configPanelFilteredRows()
	if len(rows) == 0 {
		panel.selected = 0
	} else {
		panel.selected = clampModalInt(panel.selected, 0, len(rows)-1)
		if m.configPanelOptionDisabledReason(rows[panel.selected]) != "" {
			panel.selected = m.configPanelFirstEnabledRow(rows)
		}
	}
	activeStyle := func(text string) string {
		return lipgloss.NewStyle().Bold(true).Reverse(true).Render(text)
	}
	inactiveStyle := func(text string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(text)
	}
	if panel.effects {
		return m.renderConfigEffectsPanel(rows, rowWidth, inactiveStyle)
	}
	searching := panel.search
	// Rust renders the active tab's own items while the search query is empty;
	// only actual query results carry their group tag.
	tagging := searching && strings.TrimSpace(panel.query) != ""
	resultRows := max(configPanelMaxSectionRows(), 1)
	optionBudget := min(max(m.height-12, 1), resultRows)
	title := m.modal.title
	subtitle := strings.TrimSpace(panel.notice)
	if subtitle == "" {
		subtitle = "Changes save automatically"
	}
	contentWidth := max(rowWidth-2, 1)
	tabs := bottompane.FilledTabBarStyledText(configPanelSectionTitles, int(panel.section), contentWidth, activeStyle, inactiveStyle)
	header := []string{
		"",
		"  " + title,
		"  " + codextui.TruncateWithEllipsis(subtitle, contentWidth),
		"",
		"  " + tabs,
		"",
	}
	if searching {
		query := panel.query
		if query == "" {
			query = inactiveStyle("Type to search settings")
		}
		header = append(header, "  Search \u203a "+query, "")
	}
	acceptLabel := ""
	if panel.selected >= 0 && panel.selected < len(rows) && m.configPanelRowDisabledReason(rows[panel.selected]) == "" {
		acceptLabel = configPanelAcceptLabel(rows[panel.selected])
	}
	var footer string
	if searching {
		footer = "esc clear search"
		if acceptLabel != "" {
			footer = "enter " + acceptLabel + " \u00b7 " + footer
		}
	} else {
		footer = "left/right group \u00b7 / search"
		if acceptLabel != "" {
			footer += " \u00b7 enter " + acceptLabel
		}
		footer += " \u00b7 esc clear search/close"
	}
	descCol := configPanelDescriptionColumn(rows, rowWidth)
	narrow := rowWidth-descCol < configPanelInlineDescriptionMinWidth
	start, visible, above, below := modalOptionWindow(len(rows), panel.selected, panel.scrollTop, optionBudget)
	panel.scrollTop = start
	panel.visibleRows = visible
	lines := append([]string(nil), header...)
	if above {
		lines = append(lines, "\u2191")
	}
	rendered := 0
	for index := start; index < start+visible && index < len(rows); index++ {
		row := rows[index]
		line := configPanelRowLine(row.label, m.configPanelValue(row), m.configPanelRowDescription(row, tagging), descCol, index == panel.selected, narrow)
		line = codextui.TruncateWithEllipsis(line, rowWidth)
		if index == panel.selected {
			line = codextui.RenderSelectedRowFilled(line, rowWidth)
		}
		lines = append(lines, line)
		rendered++
	}
	if len(rows) == 0 {
		lines = append(lines, "  No matching settings")
		rendered++
	}
	for rendered < resultRows {
		lines = append(lines, "")
		rendered++
	}
	if narrow {
		lines = append(lines, m.configPanelDescriptionSlot(rows, rowWidth, tagging)...)
	}
	spacer := ""
	if below {
		spacer = "\u2193"
	}
	lines = append(lines, spacer, codextui.TruncateWithEllipsis("  "+footer, rowWidth))
	return strings.Join(lines, "\n")
}

// configEffectsTogglePrefix mirrors Rust's toggle checkbox prefix, including the
// "[-]" placeholder for rows whose dependency is unmet.
func (m *Model) configEffectsTogglePrefix(row configPanelRow) string {
	if m.configEffectsRowDisabledReason(row) != "" {
		return "[-] "
	}
	on := false
	switch row.action {
	case configActionEffects:
		on = m.animationsEnabled
	case configActionComposerStars:
		on = m.effects.Starfield
	}
	if on {
		return "[x] "
	}
	return "[ ] "
}

func (m *Model) configEffectsDescriptionColumn(rows []configPanelRow, prefixes, toggles []string, width int) int {
	maxNameWidth := 0
	for index, row := range rows {
		name := row.label
		if m.configEffectsRowDisabledReason(row) != "" {
			name += " (disabled)"
		}
		maxNameWidth = max(maxNameWidth, codextui.DisplayWidth(prefixes[index]+toggles[index]+name))
	}
	return configPanelClampDescCol(maxNameWidth+2, width)
}

// renderConfigEffectsPanel mirrors Rust's Effects child picker: numbered rows,
// toggle checkboxes and the reason row for Composer stars.
func (m *Model) renderConfigEffectsPanel(rows []configPanelRow, rowWidth int, _ func(string) string) string {
	panel := m.modal.configPanel
	if len(rows) == 0 {
		panel.selected = 0
	} else {
		panel.selected = clampModalInt(panel.selected, 0, len(rows)-1)
	}
	prefixes := make([]string, len(rows))
	toggles := make([]string, len(rows))
	number := 0
	for index, row := range rows {
		marker := "  "
		if index == panel.selected {
			marker = "\u203a "
		}
		if m.configEffectsRowDisabledReason(row) != "" {
			prefixes[index] = marker + "   "
		} else {
			number++
			prefixes[index] = marker + strconv.Itoa(number) + ". "
		}
		toggles[index] = m.configEffectsTogglePrefix(row)
	}
	descCol := m.configEffectsDescriptionColumn(rows, prefixes, toggles, rowWidth)
	lines := []string{
		"  Effects",
		"  Configure visual effects",
		"",
		"",
	}
	for index, row := range rows {
		name := row.label
		description := row.description
		if reason := m.configEffectsRowDisabledReason(row); reason != "" {
			name += " (disabled)"
			description = reason
		}
		var builder strings.Builder
		builder.WriteString(prefixes[index])
		builder.WriteString(toggles[index])
		builder.WriteString(name)
		if gap := descCol - codextui.DisplayWidth(prefixes[index]+toggles[index]+name); gap > 0 {
			builder.WriteString(strings.Repeat(" ", gap))
		}
		builder.WriteString(description)
		line := codextui.TruncateWithEllipsis(builder.String(), rowWidth)
		if index == panel.selected {
			line = codextui.RenderSelectedRowFilled(line, rowWidth)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", codextui.TruncateWithEllipsis("  enter toggle \u00b7 esc back", rowWidth))
	return strings.Join(lines, "\n")
}

// configPanelRowDescription returns the description shown for a row. Disabled
// rows show the unavailable reason; search results carry their group tag so a
// match outside the active tab stays identifiable (Rust #53429 searches every
// group).
func (m *Model) configPanelRowDescription(row configPanelRow, searching bool) string {
	text := row.description
	if reason := m.configPanelRowDisabledReason(row); reason != "" {
		text = reason
	}
	if !searching || text == "" {
		return text
	}
	section := configPanelSectionTitle(row.section)
	if section == "" {
		return text
	}
	return section + " \u00b7 " + text
}

func configPanelSectionTitle(section configPanelSection) string {
	if section < 0 || int(section) >= len(configPanelSectionTitles) {
		return ""
	}
	return configPanelSectionTitles[section]
}

// configPanelAcceptLabel mirrors Rust's per-row footer hint: toggles say
// toggle, notification tests say test, and editors say open.
func configPanelAcceptLabel(row configPanelRow) string {
	switch row.action {
	case configActionTips, configActionStatusLineColors, configActionComposerStars, configActionNotifications,
		configActionStartInVimMode, configActionPasteDetection, configActionAutomaticRecaps,
		configActionCopyOnSelectClear:
		return "toggle"
	case configActionNotificationTest:
		return "test"
	default:
		return "open"
	}
}

// configPanelDescriptionSlot mirrors Rust #53429's SelectedBelowWhenNarrow
// layout: when the value column leaves fewer than 45 columns for the
// description, the selected row's description moves below the list.
func (m *Model) configPanelDescriptionSlot(rows []configPanelRow, width int, searching bool) []string {
	if m == nil || m.modal == nil || m.modal.configPanel == nil {
		return nil
	}
	const maxDescriptionSlotLines = 3
	wrapped := make([][]string, len(rows))
	reserved := 1
	for index, row := range rows {
		text := m.configPanelRowDescription(row, searching)
		if text == "" {
			continue
		}
		wrapped[index] = codextui.WrapLine(text, codextui.WrapOptions{
			Width:            max(width, 1),
			InitialIndent:    "  ",
			SubsequentIndent: "  ",
			BreakWords:       true,
		})
		reserved = max(reserved, min(len(wrapped[index]), maxDescriptionSlotLines))
	}
	slot := make([]string, 0, reserved)
	if selected := m.modal.configPanel.selected; selected >= 0 && selected < len(wrapped) {
		slot = append(slot, wrapped[selected]...)
	}
	if len(slot) > reserved {
		slot = slot[:reserved]
	}
	for len(slot) < reserved {
		slot = append(slot, "")
	}
	return slot
}
