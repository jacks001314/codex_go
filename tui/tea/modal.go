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
	"codex_go/voicehost"
)

type ModalKind string

const (
	ModalKindApproval    ModalKind = "approval"
	ModalKindElicitation ModalKind = "elicitation"
	ModalKindPicker      ModalKind = "picker"
	// ModalKindVoiceChannels hosts the microphone channel multi-select
	// (Rust #49836).
	ModalKindVoiceChannels   ModalKind = "voice_channels"
	ModalKindUserInput       ModalKind = "user_input"
	ModalKindUsage           ModalKind = "usage"
	ModalKindStatusLine      ModalKind = "status_line"
	ModalKindTitle           ModalKind = "terminal_title"
	ModalKindPermissions     ModalKind = "permissions"
	ModalKindExperimental    ModalKind = "experimental"
	ModalKindRateLimitSwitch ModalKind = "rate_limit_switch"
	ModalKindReview          ModalKind = "review"
	ModalKindSkills          ModalKind = "skills"
	ModalKindManageSkills    ModalKind = "manage_skills"
	ModalKindExternalImport  ModalKind = "external_agent_import"
	ModalKindHooksBrowser    ModalKind = "hooks_browser"
	ModalKindPluginsBrowser  ModalKind = "plugins_browser"
	ModalKindAgent           ModalKind = "agent"
	ModalKindAgents          ModalKind = "agents"
	ModalKindTheme           ModalKind = "theme"
	ModalKindPets            ModalKind = "pets"
	ModalKindMemories        ModalKind = "memories"
	ModalKindFeedback        ModalKind = "feedback"
	ModalKindAutoReview      ModalKind = "auto_review_denials"
	ModalKindGoal            ModalKind = "goal"
	ModalKindPresets         ModalKind = "presets"
	ModalKindConfig          ModalKind = "config"
	ModalKindMisalignment    ModalKind = "misalignment"
	ModalKindWindowsSandbox  ModalKind = "windows_sandbox"
	ModalKindRunningTaskExit ModalKind = "running_task_exit"
	ModalKindWorktree        ModalKind = "worktree"
	ModalKindGeneric         ModalKind = "generic"
)

type ModalOption struct {
	ID                   string
	Label                string
	Description          string
	SelectedDescription  string
	DisabledReason       string
	DisabledGutterMarker string
	Shortcut             string
	Disabled             bool
	// RequireConfirmation arms the option on its first activation and only
	// runs it on a second activation (Rust SelectionItem
	// require_explicit_confirmation, #44744). Destructive options such as
	// permanent deletion keep this; archive does not.
	RequireConfirmation bool
}

type ModalRequestMsg struct {
	ID string
	// ServerRequestID identifies the app-server request whose answer this view
	// collects. It is distinct from the local view ID used by request brokers,
	// so serverRequest/resolved can dismiss the correct modal (Rust #55682).
	ServerRequestID   string
	Kind              ModalKind
	Title             string
	Body              string
	Options           []ModalOption
	FooterNote        string
	FooterHint        string
	ReopenOnCancel    bool
	ColumnWidth       bottompane.ColumnWidthConfig
	DescriptionLayout bottompane.SelectionDescriptionLayout
}

type ApprovalRequestMsg struct {
	ID              string
	ServerRequestID string
	Title           string
	Body            string
	Command         string
	Options         []ModalOption
}

// AppLinkAction is one host-side effect of the app-link popup (Rust
// bottom_pane::app_link_view::AppLinkEvent). The model never performs these
// itself; Options.OnAppLinkAction forwards them to the host as they happen.
type AppLinkAction struct {
	Kind    bottompane.AppLinkEventKind
	URL     string
	AppID   string
	Enabled bool
}

// AppLinkActionOpenURL is the action that asks the host to open the
// install/sign-in URL in the user's browser.
const AppLinkActionOpenURL = bottompane.AppLinkEventOpenURL

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
	id              string
	serverRequestID string
	kind            ModalKind
	title           string
	body            string
	bodyOffset      int
	options         []ModalOption
	selected        int
	// optionScrollTop/optionVisibleRows track the rendered result viewport so
	// page navigation uses the rows that actually fit (Rust #46680).
	optionScrollTop   int
	optionVisibleRows int
	footerNote        string
	footerHint        string
	reopenOnCancel    bool
	columnWidth       bottompane.ColumnWidthConfig
	descriptionLayout bottompane.SelectionDescriptionLayout
	keymapCapture     *keymapCaptureState
	// armedOptionID tracks an option whose RequireConfirmation gate has been
	// passed once; a second activation runs it (Rust #44744).
	armedOptionID string

	elicitation *bottompane.ElicitationFormRequest
	// userVerification marks the openai/userVerification elicitation so its
	// title and request details carry terminal hyperlinks (Rust #51458).
	userVerification bool
	// appLink is the validated tool-suggestion popup shown in place of the plain
	// elicitation form (Rust #48015 app link view).
	appLink *bottompane.AppLinkView
	// appLinkActionsDelivered counts the view events already handed to the host.
	appLinkActionsDelivered int
	modelPicker             *codextui.ModelPicker
	modelReasoning          *codextui.ModelReasoningPicker
	planReasoningScope      *codextui.PlanReasoningScopePicker
	sessionPicker           *codextui.SessionPickerState
	sessionPickerNotice     string
	sessionAction           *codextui.SessionSelection
	exitAfterSessionAction  bool
	// unarchivePrompt hosts the archived-conversation confirmation and
	// retrySelection is the resume/fork to retry after unarchiving (Rust
	// unarchive_prompt.rs + session_start.rs).
	unarchivePrompt        *unarchivePromptState
	retrySelection         *codextui.SessionSelection
	themePicker            *codextui.ThemePicker
	themeFilter            string
	themeSubtitle          string
	themeScrollTop         int
	themeVisibleRows       int
	userInput              *codextui.RequestUserInputState
	statusLineSetup        *statusLineSetupModal
	terminalTitleSetup     *terminalTitleSetupModal
	customPrompt           *bottompane.CustomPromptView
	customPromptSubmit     func(string) bubbletea.Cmd
	goalObjective          string
	memories               *memoriesModalState
	feedback               *feedbackModalState
	configPanel            *configPanelState
	manageSkills           *manageSkillsModalState
	externalAgentMigration *externalAgentMigrationModalState
	hooksBrowser           *hooksBrowserModalState
	pluginBrowser          *pluginBrowserModalState
	worktreeBrowser        *worktreeBrowserState
	// voiceChannels hosts the microphone channel multi-select and the device it
	// belongs to (Rust #49836 open_realtime_input_channels).
	voiceChannels      *bottompane.MultiSelectPicker
	voiceChannelDevice voicehost.AudioDevice
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
		ID:              message.ID,
		ServerRequestID: message.ServerRequestID,
		Kind:            ModalKindApproval,
		Title:           firstNonEmpty(message.Title, "Approval request"),
		Body:            body,
		Options:         options,
	})
	return m.queueNotification(chatwidget.ExecApprovalRequestedNotification(message.Command))
}

func (m *Model) openModal(message ModalRequestMsg) {
	if m != nil && m.modal == nil {
		m.statusTimer.PauseAt(m.currentTime())
	}
	options := normalizeModalOptions(message.Options)
	if len(options) == 0 {
		options = []ModalOption{{ID: "ok", Label: "OK", Shortcut: "enter"}}
	}
	m.replaceModal(&modalState{
		id:                strings.TrimSpace(message.ID),
		serverRequestID:   strings.TrimSpace(message.ServerRequestID),
		kind:              message.Kind,
		title:             firstNonEmpty(message.Title, "Select an option"),
		body:              strings.TrimSpace(message.Body),
		options:           options,
		footerNote:        strings.TrimSpace(message.FooterNote),
		footerHint:        strings.TrimSpace(message.FooterHint),
		reopenOnCancel:    message.ReopenOnCancel,
		columnWidth:       message.ColumnWidth,
		descriptionLayout: message.DescriptionLayout,
	})
	m.quickDialMenu = false
	m.notice = ""
}

func (m *Model) replaceModal(next *modalState) {
	if m == nil {
		return
	}
	if m.modal != nil && m.modal != next && m.modal.kind != next.kind &&
		modalKindUsesParentBackdrop(next.kind) {
		m.modalParents = append(m.modalParents, m.modal)
	}
	m.modal = next
}

func modalKindUsesParentBackdrop(kind ModalKind) bool {
	switch kind {
	case ModalKindGeneric,
		ModalKindReview,
		ModalKindApproval,
		ModalKindElicitation,
		ModalKindUserInput,
		ModalKindFeedback,
		ModalKindRateLimitSwitch,
		ModalKindRunningTaskExit,
		ModalKindMisalignment,
		ModalKindWindowsSandbox:
		return true
	default:
		return false
	}
}

func (m *Model) restoreParentModal() {
	if m == nil || m.modal != nil || len(m.modalParents) == 0 {
		return
	}
	m.modal = m.modalParents[len(m.modalParents)-1]
	m.modalParents = m.modalParents[:len(m.modalParents)-1]
}

func (m *Model) clearModalStack() {
	if m == nil {
		return
	}
	m.modalParents = nil
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
			ID:                   id,
			Label:                label,
			Description:          strings.TrimSpace(option.Description),
			SelectedDescription:  strings.TrimSpace(option.SelectedDescription),
			DisabledReason:       strings.TrimSpace(option.DisabledReason),
			DisabledGutterMarker: strings.TrimSpace(option.DisabledGutterMarker),
			Shortcut:             strings.ToLower(strings.TrimSpace(option.Shortcut)),
			Disabled:             option.Disabled,
			RequireConfirmation:  option.RequireConfirmation,
		})
	}
	return out
}

func (m *Model) updateModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil {
		return nil
	}
	if m.modal.id == resumeCWDPromptID && message.Type == bubbletea.KeyCtrlC {
		m.modal = nil
		m.pendingResumeCWD = nil
		return bubbletea.Quit
	}
	if m.modal.configPanel != nil {
		return m.updateConfigPanelKey(message)
	}
	if m.modal.themePicker != nil {
		return m.updateThemePickerModal(message)
	}
	if m.modal.manageSkills != nil {
		return m.updateManageSkillsModal(message)
	}
	if m.modal.voiceChannels != nil {
		return m.updateVoiceChannelsModal(message)
	}
	if m.modal.externalAgentMigration != nil {
		return m.updateExternalAgentMigrationModal(message)
	}
	if m.modal.hooksBrowser != nil {
		return m.updateHooksBrowserModal(message)
	}
	if m.modal.pluginBrowser != nil {
		return m.updatePluginBrowserModal(message)
	}
	if m.modal.worktreeBrowser != nil {
		return m.updateWorktreeBrowserModal(message)
	}
	if m.modal.customPrompt != nil {
		return m.updateCustomPromptModal(message)
	}
	if m.modal.keymapCapture != nil {
		return m.updateKeymapCapture(message)
	}
	if m.modal.memories != nil {
		return m.updateMemoriesModal(message)
	}
	if m.modal.feedback != nil {
		return m.updateFeedbackModal(message)
	}
	if m.modal.unarchivePrompt != nil {
		return m.updateUnarchivePromptModal(message)
	}
	if m.modal.kind == ModalKindExperimental {
		// The popup owns its keys: toggling, saving, and the in-flight guard
		// (Rust ExperimentalFeaturesView).
		return m.updateExperimentalModal(message)
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
	if m.modal.kind == ModalKindMisalignment {
		switch message.Type {
		case bubbletea.KeyPgUp:
			m.scrollMisalignmentBody(-m.misalignmentBodyPage())
			return nil
		case bubbletea.KeyPgDown:
			m.scrollMisalignmentBody(m.misalignmentBodyPage())
			return nil
		}
	}
	switch message.Type {
	case bubbletea.KeyEsc:
		return m.respondModal(true)
	case bubbletea.KeyEnter:
		return m.activateModalOption(m.modal.selected)
	case bubbletea.KeyUp:
		m.moveModalSelection(-1)
		m.disarmModalOption()
		return nil
	case bubbletea.KeyDown, bubbletea.KeyTab:
		m.moveModalSelection(1)
		m.disarmModalOption()
		return nil
	case bubbletea.KeyPgUp:
		m.moveModalSelectionPage(-max(m.modal.optionVisibleRows, 1))
		m.disarmModalOption()
		return nil
	case bubbletea.KeyPgDown:
		m.moveModalSelectionPage(max(m.modal.optionVisibleRows, 1))
		m.disarmModalOption()
		return nil
	case bubbletea.KeyHome, bubbletea.KeyCtrlHome:
		m.selectModalEdge(false)
		m.disarmModalOption()
		return nil
	case bubbletea.KeyEnd, bubbletea.KeyCtrlEnd:
		m.selectModalEdge(true)
		m.disarmModalOption()
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
				return m.activateModalOption(i)
			}
		}
	}
	return nil
}

func (m *Model) misalignmentBodyPage() int {
	if m == nil || m.height <= 0 {
		return 10
	}
	page := m.height / 2
	if page < 3 {
		page = 3
	}
	return page
}

func (m *Model) scrollMisalignmentBody(delta int) {
	if m == nil || m.modal == nil || delta == 0 {
		return
	}
	lines := strings.Split(m.modal.body, "\n")
	maxLines := m.misalignmentBodyPage()
	maxOffset := len(lines) - maxLines
	if maxOffset < 0 {
		maxOffset = 0
	}
	m.modal.bodyOffset += delta
	if m.modal.bodyOffset < 0 {
		m.modal.bodyOffset = 0
	}
	if m.modal.bodyOffset > maxOffset {
		m.modal.bodyOffset = maxOffset
	}
}

func (m *Model) renderMisalignmentBody() string {
	if m == nil || m.modal == nil {
		return ""
	}
	body := strings.TrimRight(m.modal.body, "\r\n")
	lines := strings.Split(body, "\n")
	maxLines := m.misalignmentBodyPage()
	if len(lines) <= maxLines {
		m.modal.bodyOffset = 0
		return body
	}
	maxOffset := len(lines) - maxLines
	if m.modal.bodyOffset > maxOffset {
		m.modal.bodyOffset = maxOffset
	}
	if m.modal.bodyOffset < 0 {
		m.modal.bodyOffset = 0
	}
	start := m.modal.bodyOffset
	end := start + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	out := append([]string(nil), lines[start:end]...)
	if start > 0 {
		out = append([]string{"↑ older findings"}, out...)
	}
	if end < len(lines) {
		out = append(out, "↓ newer findings · PgUp/PgDn")
	}
	return strings.Join(out, "\n")
}

// activateModalOption selects an option and runs it, except that an option
// marked RequireConfirmation is only armed on the first activation (Rust
// #44744: destructive actions keep an explicit second confirmation while
// archiving acts immediately).
func (m *Model) activateModalOption(index int) bubbletea.Cmd {
	if m == nil || m.modal == nil || index < 0 || index >= len(m.modal.options) {
		return nil
	}
	option := m.modal.options[index]
	if option.Disabled {
		return nil
	}
	m.modal.selected = index
	if option.RequireConfirmation && m.modal.armedOptionID != option.ID {
		m.modal.armedOptionID = option.ID
		m.modal.footerNote = "Press enter again to confirm."
		return nil
	}
	m.modal.armedOptionID = ""
	m.modal.footerNote = ""
	return m.respondModal(false)
}

func (m *Model) disarmModalOption() {
	if m == nil || m.modal == nil || m.modal.armedOptionID == "" {
		return
	}
	m.modal.armedOptionID = ""
	m.modal.footerNote = ""
}

func (m *Model) updateCustomPromptModal(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.customPrompt == nil {
		return nil
	}
	modal := m.modal
	prompt := modal.customPrompt
	if message.Type == bubbletea.KeyRunes {
		if message.Paste {
			prompt.HandlePaste(string(message.Runes))
		} else {
			for _, ch := range message.Runes {
				prompt.HandleRuneAt(ch, m.currentTime())
			}
		}
	} else {
		switch message.Type {
		case bubbletea.KeyEsc:
			prompt.HandleKey("esc")
		case bubbletea.KeyCtrlC:
			prompt.HandleKey("ctrl-c")
		case bubbletea.KeyEnter:
			prompt.HandleKey("enter")
		case bubbletea.KeyBackspace:
			prompt.HandleKey("backspace")
		case bubbletea.KeyLeft:
			prompt.HandleKey("left")
		case bubbletea.KeyRight:
			prompt.HandleKey("right")
		case bubbletea.KeyUp:
			prompt.HandleKey("up")
		case bubbletea.KeyDown:
			prompt.HandleKey("down")
		case bubbletea.KeyHome:
			prompt.HandleKey("home")
		case bubbletea.KeyEnd:
			prompt.HandleKey("end")
		default:
			prompt.HandleKey(message.String())
		}
	}
	if !prompt.IsComplete() {
		return nil
	}
	m.modal = nil
	if prompt.Completion == bottompane.CustomPromptCancelled {
		return nil
	}
	name, ok := prompt.LastSubmitted()
	if !ok || modal.customPromptSubmit == nil {
		return nil
	}
	return modal.customPromptSubmit(name)
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
	case bubbletea.KeyLeft:
		picker.ChangeFocusedToolbarValueDirection(codextui.SessionFilterPrevious)
	case bubbletea.KeyRight:
		picker.ChangeFocusedToolbarValueDirection(codextui.SessionFilterNext)
	case bubbletea.KeyCtrlO:
		picker.ToggleDensity()
	case bubbletea.KeyCtrlE:
		if item, ok := picker.SelectedItem(); ok {
			wasExpanded := picker.Expanded[item.ThreadID]
			picker.ToggleExpanded(item.ThreadID)
			m.syncSessionPickerModalSelection()
			if !wasExpanded {
				// The expanded row shows the conversation preview, which is
				// loaded lazily (Rust toggle_selected_expansion).
				return m.requestTranscriptPreview(item)
			}
			return nil
		}
	case bubbletea.KeyCtrlT:
		if item, ok := picker.SelectedItem(); ok {
			// Rust open_selected_transcript: ctrl+t opens the selected session's
			// full transcript in the pager overlay.
			return m.openSelectedSessionTranscript(item)
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
	m.syncModalSelectionTargets()
}

// moveModalSelectionPage moves by enabled options without wrapping, so a page
// key never jumps from the end of a long picker back to its start.
func (m *Model) moveModalSelectionPage(delta int) {
	if m == nil || m.modal == nil || len(m.modal.options) == 0 || delta == 0 {
		return
	}
	sign := 1
	if delta < 0 {
		sign = -1
		delta = -delta
	}
	next := m.modal.selected
	for moved := 0; moved < delta; {
		candidate := next + sign
		if candidate < 0 || candidate >= len(m.modal.options) {
			break
		}
		next = candidate
		if !m.modal.options[next].Disabled {
			moved++
		}
	}
	if next == m.modal.selected || m.modal.options[next].Disabled {
		return
	}
	m.modal.selected = next
	m.syncModalSelectionTargets()
}

// selectModalEdge selects the first or last enabled option.
func (m *Model) selectModalEdge(fromEnd bool) {
	if m == nil || m.modal == nil || len(m.modal.options) == 0 {
		return
	}
	start, end, step := 0, len(m.modal.options), 1
	if fromEnd {
		start, end, step = len(m.modal.options)-1, -1, -1
	}
	for index := start; index != end; index += step {
		if !m.modal.options[index].Disabled {
			m.modal.selected = index
			m.syncModalSelectionTargets()
			return
		}
	}
}

func (m *Model) syncModalSelectionTargets() {
	if m == nil || m.modal == nil {
		return
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
	defer func() {
		m.syncStatusTimerPause(m.currentTime())
	}()
	modal := m.modal
	if modal.appLink != nil {
		return m.respondAppLinkModal(modal, cancelled)
	}
	if cancelled && modal.kind == ModalKindWindowsSandbox && modal.reopenOnCancel {
		m.notice = ""
		return nil
	}
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
	if modal.kind == ModalKindGeneric && modal.id == resumeCWDPromptID {
		return m.finishResumeCWDPrompt(response)
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
			// Archive/delete selections replace the full-screen picker with a
			// normal confirmation modal. Restore the inline terminal first.
			if modal.sessionPicker != nil && m.modal != modal {
				return m.closeSessionPickerTerminalMode(nil)
			}
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
	if modal.kind == ModalKindWindowsSandbox {
		m.modal = nil
		if cancelled {
			m.notice = ""
			return nil
		}
		return m.applyWindowsSandboxModalOption(response.OptionID)
	}
	if modal.kind == ModalKindPermissions {
		m.modal = nil
		m.permissionPopupPending = false
		if cancelled {
			m.notice = "Cancelled"
			return m.settlePermissionPopup()
		}
		return bubbletea.Batch(m.applyPermissionsModalOption(response.OptionID), m.settlePermissionPopup())
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
	if modal.kind == ModalKindAgents {
		m.modal = nil
		if modal.id == agentsOverviewLifecycleModalID {
			if cancelled {
				m.notice = "Cancelled"
				return nil
			}
			return m.applyAgentsOverviewLifecycleOption(response.OptionID)
		}
		if cancelled {
			m.notice = ""
			return nil
		}
		return m.applyAgentsModalOption(response.OptionID)
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
	if modal.kind == ModalKindAutoReview {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyAutoReviewDenialSelection(response.OptionID)
	}
	if modal.kind == ModalKindRunningTaskExit {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyRunningTaskExit(response.OptionID)
	}
	if modal.kind == ModalKindGoal {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyGoalModalOption(response.OptionID, modal.goalObjective)
	}
	if modal.kind == ModalKindPresets {
		m.modal = nil
		if cancelled {
			m.quickDialPending = nil
			m.quickDialPrefills = nil
			m.notice = "Cancelled"
			return nil
		}
		return m.applyQuickDialModalOption(modal.id, response.OptionID)
	}
	if modal.kind == ModalKindMisalignment {
		m.modal = nil
		if cancelled {
			return m.showMisalignmentPrecaution()
		}
		return m.applyMisalignmentModalOption(modal.id, response.OptionID)
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
	if modal.kind == ModalKindGeneric && isKeymapModalID(modal.id) {
		m.modal = nil
		if cancelled {
			return m.cancelKeymapModal(modal.id)
		}
		return m.applyKeymapModalOption(modal.id, response.OptionID)
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.TUIModePickerViewID {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyTUIModeModalOption(response.OptionID)
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.CopyTargetPickerViewID {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyCopyTargetModalOption(response.OptionID)
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.TranscriptExportPickerViewID {
		m.modal = nil
		if cancelled {
			m.notice = "Cancelled"
			return nil
		}
		return m.applyTranscriptExportModalOption(response.OptionID)
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.VoicePickerViewID {
		m.modal = nil
		if cancelled {
			// Rust #49437: the voice catalog is a submenu of "Voice settings",
			// so esc returns to its parent instead of closing the hierarchy.
			m.notice = ""
			m.openVoiceSettingsView()
			return nil
		}
		return m.applyVoicePickerOption(response.OptionID)
	}
	// Rust #49437/#49836: the voice settings hierarchy navigates between its
	// own views, so cancel returns to the parent menu instead of closing.
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.VoiceSettingsViewID {
		m.modal = nil
		if cancelled {
			return nil
		}
		m.notice = ""
		return m.applyVoiceSettingsOption(response.OptionID)
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.VoiceSoundDevicesViewID {
		m.modal = nil
		if cancelled {
			m.notice = ""
			m.openVoiceSettingsView()
			return nil
		}
		return m.applyVoiceSoundDevicesOption(response.OptionID)
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.VoiceDevicePickerViewID {
		m.modal = nil
		if cancelled {
			m.notice = ""
			m.openVoiceSoundDevicesView()
			return nil
		}
		return m.applyVoiceDevicePickerOption(response.OptionID)
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.SafetyBufferingPromptViewID {
		m.modal = nil
		if cancelled {
			return nil
		}
		return m.applySafetyBufferingModalOption(response.OptionID)
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.DaemonMenuViewID {
		m.modal = nil
		if cancelled {
			return nil
		}
		if response.OptionID == chatwidget.DaemonMenuItemRestart {
			return m.confirmDaemonRestart()
		}
		return m.confirmDaemonUpdate(codextui.DaemonUpdateSource(response.OptionID))
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.DaybreakPromptViewID {
		m.modal = nil
		if cancelled || response.OptionID != chatwidget.DaybreakEnableItem {
			return nil
		}
		return m.setDaybreakPreference(true)
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.SafetyRecourseViewID {
		m.modal = nil
		if cancelled {
			return nil
		}
		return m.applySafetyRecourseModalOption(response.OptionID)
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.DaemonRestartConfirmViewID {
		m.modal = nil
		if cancelled || response.OptionID != chatwidget.DaemonMenuItemRestartAndReconnect {
			return nil
		}
		return m.runDaemonRestart()
	}
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.DaemonConfirmViewID {
		m.modal = nil
		if cancelled || response.OptionID != chatwidget.DaemonMenuItemUpdateAndExit {
			return nil
		}
		return m.runDaemonUpdate(m.daemonConfirmSource)
	}
	// Rust #43074: the apps failure popup's Retry action re-runs the directory
	// request; catalog rows keep their existing modal-response handling.
	if modal.kind == ModalKindGeneric && modal.id == chatwidget.AppsSelectionViewID &&
		!cancelled && response.OptionID == string(chatwidget.AppsActionRetry) {
		m.modal = nil
		m.notice = ""
		return m.applyAppsCommand()
	}
	m.modal = nil
	if !cancelled && len(modal.options) > 0 {
		if notice != "" {
			m.notice = notice
		} else {
			m.notice = modal.options[modal.selected].Label
		}
	}
	var callback bubbletea.Cmd
	if m.onModalResponse != nil {
		callback = m.onModalResponse(response)
	}
	if modal.exitAfterSessionAction && !cancelled && response.Picker != nil {
		if modal.sessionAction != nil &&
			modal.sessionAction.Kind == codextui.SessionSelectionDelete &&
			m.sharedServerCommandCenterAvailable() {
			return m.closeSessionPickerTerminalMode(bubbletea.Sequence(callback, m.applyAgentsCommand()))
		}
		return m.closeSessionPickerTerminalMode(bubbletea.Sequence(callback, bubbletea.Quit))
	}
	if modal.sessionPicker != nil {
		return m.closeSessionPickerTerminalMode(callback)
	}
	return callback
}

// respondAppLinkModal drives the validated tool-suggestion popup: the selected
// option activates the app-link view's action, the resulting host-side effects
// reach the host, and the popup closes (resolving the elicitation) once the view
// completes. Mirrors Rust's bottom-pane app link view over AppEventSender
// (#48015).
func (m *Model) respondAppLinkModal(modal *modalState, cancelled bool) bubbletea.Cmd {
	view := modal.appLink
	if view == nil {
		m.modal = nil
		return nil
	}
	if cancelled {
		view.Cancel()
	} else {
		labels := view.ActionLabels()
		if modal.selected < 0 || modal.selected >= len(labels) {
			return nil
		}
		view.SelectedAction = modal.selected
		view.ActivateSelectedAction()
	}
	actions := m.deliverAppLinkActions(modal, view)
	if view.IsComplete() {
		m.modal = nil
		response := ModalResponse{ID: modal.id, Kind: modal.kind}
		if decision := appLinkElicitationDecision(view); decision != nil {
			response.Elicitation = decision
		}
		callback := m.runAppLinkActions(actions)
		if m.onModalResponse == nil {
			return callback
		}
		return bubbletea.Sequence(callback, m.onModalResponse(response))
	}
	// The flow moved to its confirmation screen; refresh the popup in place.
	modal.body = strings.Join(view.Rows(firstPositive(m.width-4, 76)), "\n")
	modal.options = appLinkModalOptions(view)
	modal.selected = 0
	return m.runAppLinkActions(actions)
}

// runAppLinkActions hands the popup's host-side effects to the host.
func (m *Model) runAppLinkActions(actions []AppLinkAction) bubbletea.Cmd {
	if m == nil || m.onAppLinkAction == nil || len(actions) == 0 {
		return nil
	}
	commands := make([]bubbletea.Cmd, 0, len(actions))
	for _, action := range actions {
		if command := m.onAppLinkAction(action); command != nil {
			commands = append(commands, command)
		}
	}
	if len(commands) == 0 {
		return nil
	}
	return bubbletea.Sequence(commands...)
}

// deliverAppLinkActions returns the view events not yet handed to the host.
func (m *Model) deliverAppLinkActions(modal *modalState, view *bottompane.AppLinkView) []AppLinkAction {
	events := view.Events()
	if modal.appLinkActionsDelivered >= len(events) {
		return nil
	}
	pending := events[modal.appLinkActionsDelivered:]
	modal.appLinkActionsDelivered = len(events)
	actions := make([]AppLinkAction, 0, len(pending))
	for _, event := range pending {
		actions = append(actions, AppLinkAction{
			Kind:    event.Kind,
			URL:     event.URL,
			AppID:   event.AppID,
			Enabled: event.Enabled,
		})
	}
	return actions
}

// appLinkElicitationDecision maps a completed app-link view onto the
// elicitation decision the host resolves with.
func appLinkElicitationDecision(view *bottompane.AppLinkView) *ElicitationDecision {
	if view == nil {
		return nil
	}
	for _, event := range view.Events() {
		if event.Kind != bottompane.AppLinkEventResolveElicitation {
			continue
		}
		return &ElicitationDecision{Action: string(event.Decision)}
	}
	return nil
}

// appLinkModalOptions renders the app-link view's actions as modal options.
func appLinkModalOptions(view *bottompane.AppLinkView) []ModalOption {
	labels := view.ActionLabels()
	options := make([]ModalOption, 0, len(labels))
	for i, label := range labels {
		options = append(options, ModalOption{ID: fmt.Sprintf("app_link_%d", i), Label: label})
	}
	return options
}

func (m *Model) closeSessionPickerTerminalMode(next bubbletea.Cmd) bubbletea.Cmd {
	if m == nil || !m.sessionPickerAltScreen {
		return next
	}
	m.sessionPickerAltScreen = false
	return bubbletea.Sequence(bubbletea.ExitAltScreen, next)
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
		if otherSelected && !modal.userInput.NotesVisible {
			// Rust #38624: accepting the generated "Other" option opens the
			// notes editor instead of submitting the response; the answer is
			// committed only when notes are submitted afterward.
			modal.userInput.BeginNotes()
			modal.userInput.Draft = ""
			m.refreshRequestUserInputModal()
			return nil, false, false
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
		m.refreshServiceTierCommands()
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
		m.refreshServiceTierCommands()
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
	// A manual model change releases any Reserve-switch hold.
	m.clearRateLimitRecoveryHold()
	m.refreshServiceTierCommands()
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
	case bubbletea.KeyPgUp:
		m.moveThemePickerFilteredBy(-max(modal.themeVisibleRows, 1))
		return nil
	case bubbletea.KeyPgDown:
		m.moveThemePickerFilteredBy(max(modal.themeVisibleRows, 1))
		return nil
	case bubbletea.KeyHome, bubbletea.KeyCtrlHome:
		picker.SelectFirstFiltered(modal.themeFilter)
		modal.themeScrollTop = 0
		return nil
	case bubbletea.KeyEnd, bubbletea.KeyCtrlEnd:
		indices := picker.FilteredIndices(modal.themeFilter)
		if len(indices) > 0 {
			picker.Select(indices[len(indices)-1])
			modal.themeScrollTop = max(len(indices)-max(modal.themeVisibleRows, 1), 0)
		}
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

func (m *Model) moveThemePickerFilteredBy(delta int) {
	if m == nil || m.modal == nil || m.modal.themePicker == nil {
		return
	}
	picker := m.modal.themePicker
	indices := picker.FilteredIndices(m.modal.themeFilter)
	if len(indices) == 0 {
		return
	}
	position := 0
	for index, themeIndex := range indices {
		if themeIndex == picker.Selected {
			position = index
			break
		}
	}
	position = clampModalInt(position+delta, 0, len(indices)-1)
	picker.Select(indices[position])
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

// modalLineCount counts rendered rows after dropping trailing newlines.
func modalLineCount(text string) int {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func clampModalInt(value int, low int, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// modalOptionBudget returns the rows available to the picker's result
// viewport. The status and footer rows stay visible and the transcript keeps
// its minimum height.
func (m *Model) modalOptionBudget(prefixRows int, suffixRows int) int {
	maxRows := max(m.height-6, 4)
	return max(maxRows-prefixRows-suffixRows, 1)
}

// modalOptionWindow keeps the selected option visible and reserves rows for
// the ↑/↓ overflow indicators when the result viewport is smaller than the
// option list.
func modalOptionWindow(total int, selected int, scrollTop int, budget int) (start int, rows int, above bool, below bool) {
	if total <= 0 {
		return 0, 0, false, false
	}
	if budget <= 0 {
		budget = 1
	}
	if total <= budget {
		return 0, total, false, false
	}
	rows = budget
	if budget >= 3 {
		rows = budget - 2
	}
	rows = max(rows, 1)
	selected = clampModalInt(selected, 0, total-1)
	start = clampModalInt(scrollTop, 0, total-rows)
	if selected < start {
		start = selected
	}
	if selected >= start+rows {
		start = selected + 1 - rows
	}
	start = clampModalInt(start, 0, total-rows)
	return start, rows, start > 0, start+rows < total
}

func (m *Model) renderModal() string {
	if m == nil || m.modal == nil {
		return ""
	}
	if m.modal.configPanel != nil {
		return m.renderConfigPanel()
	}
	if m.modal.kind == ModalKindWindowsSandbox {
		return m.renderWindowsSandboxModal()
	}
	if m.modal.themePicker != nil {
		return m.renderThemePickerModal()
	}
	if m.modal.manageSkills != nil {
		return m.renderManageSkillsModal()
	}
	if m.modal.voiceChannels != nil {
		return m.renderVoiceChannelsModal()
	}
	if m.modal.externalAgentMigration != nil {
		return m.renderExternalAgentMigrationModal()
	}
	if m.modal.hooksBrowser != nil {
		return m.renderHooksBrowserModal()
	}
	if m.modal.pluginBrowser != nil {
		return m.renderPluginBrowserModal()
	}
	if m.modal.worktreeBrowser != nil {
		return m.renderWorktreeBrowserModal()
	}
	if m.modal.customPrompt != nil {
		return strings.Join(m.modal.customPrompt.Rows(), "\n")
	}
	if m.modal.feedback != nil && m.modal.feedback.stage == feedbackStageNote {
		return m.renderFeedbackNoteModal()
	}
	if m.modal.keymapCapture != nil {
		return m.renderKeymapCapture()
	}
	if m.modal.unarchivePrompt != nil {
		return m.modal.unarchivePrompt.render()
	}
	if m.modal.sessionPicker != nil {
		return m.renderSessionPickerModal()
	}
	var builder strings.Builder
	title := m.modal.title
	body := m.modal.body
	footerNote := m.modal.footerNote
	if m.modal.userVerification {
		// Rust #51458 renders the verification title and request details with
		// HyperlinkText, so URLs stay clickable and keep their complete target.
		title = annotationWrappedText(title, max(m.width-2, 8))
		body = annotationWrappedText(body, max(m.width-4, 8))
	}
	if m.modal.id == chatwidget.SafetyBufferingPromptViewID {
		title = strings.Join(codextui.WrapLines([]string{title}, codextui.WrapOptions{
			Width:      max(m.width-2, 8),
			BreakWords: true,
		}), "\n")
		body = strings.Join(codextui.WrapLines(strings.Split(body, "\n"), codextui.WrapOptions{
			Width:      max(m.width-4, 8),
			BreakWords: true,
		}), "\n")
		footerNote = strings.Join(codextui.WrapLines([]string{footerNote}, codextui.WrapOptions{
			Width:      max(m.width-4, 8),
			BreakWords: true,
		}), "\n")
	}
	builder.WriteString(title)
	builder.WriteString("\n")
	if m.modal.kind == ModalKindMisalignment {
		body = m.renderMisalignmentBody()
	}
	if body != "" {
		builder.WriteString(indentLines(body, "  "))
		builder.WriteString("\n")
	}
	footer := firstNonEmpty(m.modal.footerHint, "Esc cancel | Enter choose")
	if m.modal.statusLineSetup != nil || m.modal.terminalTitleSetup != nil {
		footer = firstNonEmpty(m.modal.footerHint, "space toggle \u00b7 enter save \u00b7 esc cancel")
	} else if m.modal.kind == ModalKindExperimental {
		footer = firstNonEmpty(m.modal.footerHint, "space toggle \u00b7 enter save \u00b7 esc save/close")
	}
	if m.modal.userInput != nil {
		switch {
		case m.modal.userInput.ConfirmUnanswered:
			footer = "Press enter to confirm or esc to go back"
		case m.modal.userInput.NotesVisible:
			footer = "Tab or Esc clear notes | Enter submit answer"
		}
	}
	prefixRows := modalLineCount(builder.String())
	suffixRows := modalLineCount(footerNote) + modalLineCount(footer)
	optionBudget := m.modalOptionBudget(prefixRows, suffixRows)

	// A modal whose picker hides narrow descriptions renders through the
	// responsive option renderer (Rust #46691 replaced stacking with hiding).
	if m.modal.descriptionLayout.Mode == bottompane.SelectionDescriptionHideWhenNarrow {
		builder.WriteString(m.renderResponsiveModalOptions(optionBudget))
		builder.WriteString("\n")
		if footerNote != "" {
			builder.WriteString(footerNote)
			builder.WriteString("\n")
		}
		builder.WriteString(footer)
		return strings.TrimRight(builder.String(), "\n")
	}
	start, visible, above, below := modalOptionWindow(
		len(m.modal.options),
		m.modal.selected,
		m.modal.optionScrollTop,
		optionBudget,
	)
	m.modal.optionScrollTop = start
	m.modal.optionVisibleRows = visible
	if above {
		builder.WriteString("\u2191\n")
	}
	for i := start; i < start+visible && i < len(m.modal.options); i++ {
		option := m.modal.options[i]
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
		description := option.Description
		if option.DisabledReason != "" {
			if description != "" {
				description += "\n"
			}
			description += option.DisabledReason
		}
		if description != "" {
			line += " - " + description
		}
		if selected {
			line = codextui.RenderSelectedRow(line)
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	if below {
		builder.WriteString("\u2193\n")
	}
	if footerNote != "" {
		builder.WriteString(footerNote)
		builder.WriteString("\n")
	}
	builder.WriteString(footer)
	return strings.TrimRight(builder.String(), "\n")
}

// annotationWrappedText wraps each explicit line and annotates complete web
// URLs with OSC-8 targets while leaving other text unchanged.
func annotationWrappedText(text string, width int) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	lines := codextui.WrapLines(strings.Split(text, "\n"), codextui.WrapOptions{
		Width:      max(width, 1),
		BreakWords: true,
	})
	for i := range lines {
		lines[i] = codextui.AnnotateCompleteWebURLsInLine(lines[i])
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderResponsiveModalOptions(optionBudget int) string {
	if m == nil || m.modal == nil {
		return ""
	}
	enabledNumber := 0
	enabledCount := 0
	for _, option := range m.modal.options {
		if !option.Disabled {
			enabledCount++
		}
	}
	numberWidth := len(fmt.Sprintf("%d", max(enabledCount, 1)))
	rows := make([]bottompane.GenericDisplayRow, 0, len(m.modal.options))
	for index, option := range m.modal.options {
		prefix := ""
		if option.Disabled {
			markerWidth := codextui.DisplayWidth(option.DisabledGutterMarker)
			if option.DisabledGutterMarker != "" {
				prefix = codextui.SelectionPrefix(false) + strings.Repeat(" ", max(numberWidth-markerWidth, 0)) + option.DisabledGutterMarker + "  "
			} else {
				prefix = codextui.SelectionPrefix(false) + strings.Repeat(" ", numberWidth+2)
			}
		} else {
			enabledNumber++
			prefix = codextui.SelectionPrefix(index == m.modal.selected) + fmt.Sprintf("%*d. ", numberWidth, enabledNumber)
		}
		description := option.Description
		if index == m.modal.selected && option.SelectedDescription != "" {
			description = option.SelectedDescription
		}
		rows = append(rows, bottompane.GenericDisplayRow{
			Name:           option.Label,
			NamePrefix:     prefix,
			Description:    description,
			DisabledReason: option.DisabledReason,
			IsDisabled:     option.Disabled,
		})
	}
	width := m.width - 4
	if width <= 0 {
		width = max(m.width, 1)
	}
	start, visible, above, below := modalOptionWindow(
		len(rows),
		m.modal.selected,
		m.modal.optionScrollTop,
		optionBudget,
	)
	var lines []string
	for {
		state := bottompane.ScrollState{
			SelectedIdx:  m.modal.selected,
			ScrollTop:    start,
			HasSelection: true,
		}
		lines = bottompane.RenderGenericRowsWithDescriptionLayout(
			rows,
			state,
			visible,
			"",
			width,
			m.modal.columnWidth,
			m.modal.descriptionLayout,
		)
		indicatorRows := 0
		if above {
			indicatorRows++
		}
		if below {
			indicatorRows++
		}
		if len(lines)+indicatorRows <= optionBudget || visible <= 1 {
			break
		}
		start, visible, above, below = modalOptionWindow(
			len(rows),
			m.modal.selected,
			start,
			max(visible+indicatorRows-1, 1),
		)
	}
	m.modal.optionScrollTop = start
	m.modal.optionVisibleRows = visible
	if above {
		lines = append([]string{"\u2191"}, lines...)
	}
	if below {
		lines = append(lines, "\u2193")
	}
	return strings.Join(lines, "\n")
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
	if notice := strings.TrimSpace(m.modal.sessionPickerNotice); notice != "" {
		builder.WriteString(notice)
		builder.WriteString("\n\n")
	}
	rows := picker.RenderRows(contentWidth, m.currentTime())
	maxRows := sessionPickerListHeight(m.height)
	if maxRows > 0 && len(rows) > maxRows {
		rows = rows[:maxRows]
	}
	for index := 0; index < maxRows; index++ {
		if index < len(rows) {
			builder.WriteString(rows[index])
		}
		builder.WriteString("\n")
	}
	for _, row := range picker.FooterLines(contentWidth+1, true) {
		builder.WriteString(row)
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

func sessionPickerListHeight(height int) int {
	if height <= 0 {
		return 10
	}
	// Rust reserves four rows above the list and four for the footer.
	value := height - 8
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
	picker := m.modal.themePicker
	previewWidth := layout.SideWidth
	if previewWidth > 2 {
		previewWidth -= 2
	}
	var previewRows []string
	if layout.Wide {
		previewRows = renderThemePreviewRows(codextui.ThemePreviewRows(previewWidth), previewWidth, picker.PreviewThemeID())
	} else {
		previewRows = renderThemePreviewRows(codextui.NarrowThemePreviewRows(contentWidth), contentWidth, picker.PreviewThemeID())
	}
	maxRows := max(m.height-6, 8)
	subtitleRows := 0
	if subtitle != "" {
		subtitleRows = 1
	}
	// Controls and at least a few results take precedence over the preview.
	if !layout.Wide && previewRows != nil {
		required := 4 + subtitleRows + 1 + 3 + 1 + len(previewRows)
		if maxRows < required {
			previewRows = nil
		}
	}
	themeBudget := maxRows - 4 - subtitleRows - 1 // title/blank/blank/footer + prompt
	if !layout.Wide && previewRows != nil {
		themeBudget -= len(previewRows) + 1
	}
	themeBudget = max(themeBudget, 1)

	indices := picker.FilteredIndices(m.modal.themeFilter)
	selectedPosition := 0
	for position, index := range indices {
		if index == picker.Selected {
			selectedPosition = position
			break
		}
	}
	start, visible, above, below := modalOptionWindow(
		len(indices),
		selectedPosition,
		m.modal.themeScrollTop,
		themeBudget,
	)
	m.modal.themeScrollTop = start
	m.modal.themeVisibleRows = visible
	prompt := renderThemePickerSearchPrompt(m.modal.themeFilter, listWidth)
	listRows := []string{prompt}
	if above {
		listRows = append(listRows, "\u2191")
	}
	if len(indices) == 0 {
		listRows = append(listRows, renderThemePickerDimLine("  no matches", listWidth))
	} else {
		listRows = append(listRows, m.renderThemePickerRows(indices[start:start+visible], listWidth)...)
	}
	if below {
		listRows = append(listRows, "\u2193")
	}
	if layout.Wide {
		builder.WriteString(joinThemePickerColumns(listRows, previewRows, listWidth, previewWidth))
	} else {
		for _, row := range listRows {
			builder.WriteString(row)
			builder.WriteString("\n")
		}
		if previewRows != nil {
			builder.WriteString("\n")
			for _, row := range previewRows {
				builder.WriteString(row)
				builder.WriteString("\n")
			}
		}
	}
	builder.WriteString("\n")
	builder.WriteString(renderThemePickerDimLine(firstNonEmpty(m.modal.footerHint, "enter select \u00b7 esc back"), contentWidth))
	return strings.TrimRight(builder.String(), "\n")
}

func (m *Model) renderThemePickerRows(indices []int, width int) []string {
	if m == nil || m.modal == nil || m.modal.themePicker == nil {
		return nil
	}
	picker := m.modal.themePicker
	rows := make([]string, 0, len(indices))
	for _, index := range indices {
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
