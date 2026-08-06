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
	SetupChoiceRequired bool
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
	view := chatwidget.NewWindowsSandboxEnablePromptView(prompt.AllowUnelevated, prompt.SetupChoiceRequired)
	view.ViewID = windowsSandboxEnablePromptModalID
	m.openSelectionViewModal(ModalKindWindowsSandbox, view)
}

func (m *Model) openWindowsSandboxFallbackPrompt() {
	if m == nil {
		return
	}
	allowUnelevated := chatwidget.WindowsSandboxModeAllowed(m.permissionRequirements, chatwidget.WindowsSandboxModeUnelevated)
	setupChoiceRequired := m.windowsSandboxSetupChoiceRequired || !allowUnelevated
	view := chatwidget.NewWindowsSandboxFallbackPromptView(allowUnelevated, setupChoiceRequired)
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
	if m.windowsSandboxSetup == nil {
		m.notice = "Default sandbox setup is only available in the Windows app runtime."
		m.refreshTranscript()
		return nil
	}
	m.startWindowsSandboxSetupStatus()
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

func (m *Model) startWindowsSandboxSetupStatus() {
	if m == nil {
		return
	}
	status := chatwidget.WindowsSandboxSetupInProgressStatus()
	m.windowsSandboxSetupActive = true
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

func (m *Model) applyWindowsSandboxSetupResult(msg WindowsSandboxSetupResultMsg) {
	if m == nil {
		return
	}
	if msg.Err != nil {
		m.clearWindowsSandboxSetupStatus()
		if msg.Mode == chatwidget.WindowsSandboxModeElevated {
			m.openWindowsSandboxFallbackPrompt()
			return
		}
		m.notice = "Windows sandbox setup failed: " + strings.TrimSpace(msg.Err.Error())
		m.refreshTranscript()
		return
	}
	if !msg.Outcome.Started {
		m.clearWindowsSandboxSetupStatus()
		m.notice = "Windows sandbox setup is already running."
		m.refreshTranscript()
		return
	}
	if msg.Outcome.Completion != nil {
		m.applyWindowsSandboxSetupCompleted(*msg.Outcome.Completion)
		return
	}
	m.startWindowsSandboxSetupStatus()
}

func (m *Model) applyWindowsSandboxSetupCompleted(completion WindowsSandboxSetupCompletion) {
	if m == nil {
		return
	}
	m.clearWindowsSandboxSetupStatus()
	if completion.Success {
		m.notice = "Windows sandbox setup completed."
	} else if completion.Mode == chatwidget.WindowsSandboxModeElevated {
		m.openWindowsSandboxFallbackPrompt()
		return
	} else if strings.TrimSpace(completion.Error) != "" {
		m.notice = "Windows sandbox setup failed: " + strings.TrimSpace(completion.Error)
	} else {
		m.notice = "Windows sandbox setup failed."
	}
	m.refreshTranscript()
}

func (m *Model) clearWindowsSandboxSetupStatus() {
	if m == nil {
		return
	}
	m.windowsSandboxSetupActive = false
	m.windowsSandboxSetupStatus = chatwidget.WindowsSandboxSetupClearedStatus()
}

func (m *Model) applySandboxReadDirResult(result SandboxReadDirResultMsg) {
	if m == nil {
		return
	}
	if result.Err != nil {
		m.notice = "Error: " + result.Err.Error()
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return
	}
	path := strings.TrimSpace(result.CanonicalPath)
	if path == "" {
		path = strings.TrimSpace(result.RequestedPath)
	}
	m.notice = "Sandbox read access granted for " + path
	m.addInfoHistoryMessage(m.notice)
	m.refreshTranscript()
}
