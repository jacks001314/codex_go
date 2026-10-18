package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

const (
	windowsSandboxDisallowedNotice      = "That Windows sandbox option is disallowed by requirements."
	windowsSandboxEnablePromptModalID   = "windows-sandbox-enable"
	windowsSandboxFallbackPromptModalID = "windows-sandbox-fallback"
)

type WindowsSandboxStartupPrompt struct {
	AllowUnelevated     bool
	AllowElevated       bool
	SetupChoiceRequired bool
}

// WindowsSandboxState is the app-server-owned configuration used for Windows
// sandbox decisions (Rust #45821 WindowsSandboxConfig).
type WindowsSandboxState struct {
	Level        chatwidget.WindowsSandboxLevel
	Requirements chatwidget.PermissionRequirements
}

// WindowsSandboxStateReaderFunc refreshes the sandbox state for a directory.
type WindowsSandboxStateReaderFunc func(cwd string) (WindowsSandboxState, error)

// WindowsSandboxStateMsg carries a refreshed sandbox state.
type WindowsSandboxStateMsg struct {
	State WindowsSandboxState
	Err   error
}

// WindowsSandboxSetupValidatedMsg continues a setup request after the model
// re-read the app-server configuration.
type WindowsSandboxSetupValidatedMsg struct {
	Mode  chatwidget.WindowsSandboxMode
	State WindowsSandboxState
	Err   error
}

type WindowsSandboxSetupOutcome struct {
	Started    bool
	Completion *WindowsSandboxSetupCompletion
}

type WindowsSandboxSetupCompletion struct {
	Mode    chatwidget.WindowsSandboxMode
	Success bool
	Error   string
}

func (m *Model) openWindowsSandboxEnablePrompt(prompt WindowsSandboxStartupPrompt) {
	if m == nil {
		return
	}
	m.windowsSandboxSetupChoiceRequired = prompt.SetupChoiceRequired
	allowElevated := prompt.AllowElevated ||
		chatwidget.WindowsSandboxModeAllowed(m.permissionRequirements, chatwidget.WindowsSandboxModeElevated)
	view := chatwidget.NewWindowsSandboxEnablePromptView(prompt.AllowUnelevated, prompt.SetupChoiceRequired, allowElevated)
	view.ViewID = windowsSandboxEnablePromptModalID
	m.openSelectionViewModal(ModalKindWindowsSandbox, view)
}

func (m *Model) openWindowsSandboxFallbackPrompt() {
	if m == nil {
		return
	}
	allowUnelevated := chatwidget.WindowsSandboxModeAllowed(m.permissionRequirements, chatwidget.WindowsSandboxModeUnelevated)
	allowElevated := chatwidget.WindowsSandboxModeAllowed(m.permissionRequirements, chatwidget.WindowsSandboxModeElevated)
	setupChoiceRequired := m.windowsSandboxSetupChoiceRequired || !allowUnelevated
	view := chatwidget.NewWindowsSandboxFallbackPromptView(allowUnelevated, setupChoiceRequired, allowElevated)
	view.ViewID = windowsSandboxFallbackPromptModalID
	m.openSelectionViewModal(ModalKindWindowsSandbox, view)
}

func (m *Model) applyWindowsSandboxModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch chatwidget.UsageMenuAction(strings.TrimSpace(optionID)) {
	case chatwidget.WindowsSandboxActionSetupElevated:
		return m.applyWindowsSandboxSetupCommand(chatwidget.WindowsSandboxModeElevated)
	case chatwidget.WindowsSandboxActionUseLegacy:
		return m.applyWindowsSandboxSetupCommand(chatwidget.WindowsSandboxModeUnelevated)
	case chatwidget.WindowsSandboxActionQuit:
		return bubbletea.Quit
	default:
		m.notice = "Unknown Windows sandbox option."
		m.refreshTranscript()
		return nil
	}
}

func (m *Model) renderWindowsSandboxModal() string {
	if m == nil || m.modal == nil {
		return ""
	}
	width := max(m.width-4, 1)
	var builder strings.Builder
	if body := strings.TrimSpace(m.modal.body); body != "" {
		wrapped := codextui.WrapLines(strings.Split(body, "\n"), codextui.WrapOptions{
			Width:        width,
			BreakWords:   true,
			PreserveURLs: true,
		})
		builder.WriteString(strings.Join(wrapped, "\n"))
		builder.WriteString("\n\n")
	}
	for index, option := range m.modal.options {
		line := codextui.NumberedSelectionPrefix(index, index == m.modal.selected) + option.Label
		if index == m.modal.selected {
			line = codextui.RenderSelectedRow(line)
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	builder.WriteString(firstNonEmpty(m.modal.footerHint, "Press enter to confirm or esc to go back"))
	return strings.TrimRight(builder.String(), "\n")
}

func (m *Model) applyWindowsSandboxSetupCommand(mode chatwidget.WindowsSandboxMode) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if !chatwidget.WindowsSandboxModeAllowed(m.permissionRequirements, mode) {
		m.notice = windowsSandboxDisallowedNotice
		m.refreshTranscript()
		return nil
	}
	if m.onReadWindowsSandboxState != nil {
		// Rust #45821: re-read the server's requirements before setup so a
		// directory or thread change cannot leave a stale decision.
		reader := m.onReadWindowsSandboxState
		cwd := strings.TrimSpace(m.sessionCWD)
		if cwd == "" && m.State != nil {
			cwd = strings.TrimSpace(m.State.CWD)
		}
		return func() bubbletea.Msg {
			state, err := reader(cwd)
			return WindowsSandboxSetupValidatedMsg{Mode: mode, State: state, Err: err}
		}
	}
	return m.startWindowsSandboxSetupCommand(mode)
}

func (m *Model) startWindowsSandboxSetupCommand(mode chatwidget.WindowsSandboxMode) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.windowsSandboxSetup == nil {
		m.notice = "Default sandbox setup is only available in the Windows app runtime."
		m.refreshTranscript()
		return nil
	}
	m.startWindowsSandboxSetupStatus(mode)
	setup := m.windowsSandboxSetup
	cwd := strings.TrimSpace(m.sessionCWD)
	return func() bubbletea.Msg {
		outcome, err := setup(mode, cwd)
		return WindowsSandboxSetupResultMsg{
			Mode:    mode,
			Outcome: outcome,
			Err:     err,
		}
	}
}

// applyWindowsSandboxSetupValidated continues a setup request after the
// app-server state refresh.
func (m *Model) applyWindowsSandboxSetupValidated(message WindowsSandboxSetupValidatedMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if message.Err != nil {
		m.clearWindowsSandboxSetupStatus()
		m.notice = "Could not read Windows sandbox configuration and requirements from the app server."
		m.refreshTranscript()
		return nil
	}
	m.applyWindowsSandboxState(message.State)
	if !chatwidget.WindowsSandboxModeAllowed(m.permissionRequirements, message.Mode) {
		m.notice = windowsSandboxDisallowedNotice
		m.refreshTranscript()
		return nil
	}
	return m.startWindowsSandboxSetupCommand(message.Mode)
}

// applyWindowsSandboxState adopts the app-server's level and requirements.
func (m *Model) applyWindowsSandboxState(state WindowsSandboxState) {
	if m == nil {
		return
	}
	m.windowsSandboxLevel = state.Level
	m.permissionRequirements = state.Requirements
}

// refreshWindowsSandboxStateCmd re-reads the app-server state after a thread,
// working-directory, or setup change (Rust #45821).
func (m *Model) refreshWindowsSandboxStateCmd() bubbletea.Cmd {
	if m == nil || m.onReadWindowsSandboxState == nil {
		return nil
	}
	cwd := strings.TrimSpace(m.sessionCWD)
	if cwd == "" && m.State != nil {
		cwd = strings.TrimSpace(m.State.CWD)
	}
	reader := m.onReadWindowsSandboxState
	m.windowsSandboxRefreshPending = false
	return func() bubbletea.Msg {
		state, err := reader(cwd)
		return WindowsSandboxStateMsg{State: state, Err: err}
	}
}

// applyWindowsSandboxStateMsg adopts a background refresh, preserving the
// previous requirements and reporting read failures (Rust #45821).
func (m *Model) applyWindowsSandboxStateMsg(message WindowsSandboxStateMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if message.Err != nil {
		m.addErrorHistoryMessage("Could not read Windows sandbox configuration and requirements from the app server.")
		m.refreshTranscript()
		return nil
	}
	m.applyWindowsSandboxState(message.State)
	m.refreshTranscript()
	return nil
}

func (m *Model) startWindowsSandboxSetupStatus(mode chatwidget.WindowsSandboxMode) {
	if m == nil {
		return
	}
	status := chatwidget.WindowsSandboxSetupInProgressStatus()
	m.windowsSandboxSetupActive = true
	m.statusTimer.ResetAt(0, m.currentTime())
	m.windowsSandboxSetupMode = mode
	m.windowsSandboxSetupStatus = status
	m.notice = status.Status
	m.refreshTranscript()
}

func (m *Model) renderWindowsSandboxSetupStatus() string {
	if m == nil {
		return ""
	}
	status := m.windowsSandboxSetupStatus
	lines := []string{}
	if strings.TrimSpace(status.Status) != "" {
		lines = append(lines, strings.TrimSpace(status.Status))
	}
	if strings.TrimSpace(status.Details) != "" {
		lines = append(lines, strings.TrimSpace(status.Details))
	}
	if strings.TrimSpace(status.ComposerPlaceholder) != "" {
		lines = append(lines, strings.TrimSpace(status.ComposerPlaceholder))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) applyWindowsSandboxSetupResult(msg WindowsSandboxSetupResultMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if msg.Err != nil {
		m.clearWindowsSandboxSetupStatus()
		if msg.Mode == chatwidget.WindowsSandboxModeElevated {
			m.openWindowsSandboxFallbackPrompt()
			return nil
		}
		m.notice = "Windows sandbox setup failed: " + strings.TrimSpace(msg.Err.Error())
		m.refreshTranscript()
		return nil
	}
	if !msg.Outcome.Started {
		m.clearWindowsSandboxSetupStatus()
		m.notice = "Windows sandbox setup is already running."
		m.refreshTranscript()
		return nil
	}
	if msg.Outcome.Completion != nil {
		m.windowsSandboxSetupActive = true
		m.statusTimer.ResetAt(0, m.currentTime())
		m.windowsSandboxSetupMode = msg.Mode
		return m.applyWindowsSandboxSetupCompleted(*msg.Outcome.Completion)
	}
	m.startWindowsSandboxSetupStatus(msg.Mode)
	return nil
}

func (m *Model) applyWindowsSandboxSetupCompleted(completion WindowsSandboxSetupCompletion) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	// Rust #44945: ignore a completion notification for a different mode. The
	// pending setup keeps its locked input and status until its own completion
	// (or an explicit restart) arrives.
	if m.windowsSandboxSetupActive &&
		completion.Mode != "" &&
		m.windowsSandboxSetupMode != "" &&
		completion.Mode != m.windowsSandboxSetupMode {
		return nil
	}
	m.clearWindowsSandboxSetupStatus()
	if completion.Success {
		m.notice = "Windows sandbox setup completed."
	} else if completion.Mode == chatwidget.WindowsSandboxModeElevated {
		m.openWindowsSandboxFallbackPrompt()
		return nil
	} else if strings.TrimSpace(completion.Error) != "" {
		m.notice = "Windows sandbox setup failed: " + strings.TrimSpace(completion.Error)
	} else {
		m.notice = "Windows sandbox setup failed."
	}
	m.refreshTranscript()
	// Rust #45821: refresh the server-owned configuration after setup.
	return m.refreshWindowsSandboxStateCmd()
}

func (m *Model) clearWindowsSandboxSetupStatus() {
	if m == nil {
		return
	}
	m.windowsSandboxSetupActive = false
	m.windowsSandboxSetupMode = ""
	m.windowsSandboxSetupStatus = chatwidget.WindowsSandboxSetupClearedStatus()
}
