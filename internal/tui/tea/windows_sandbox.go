package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	chatwidget "codex_go/internal/tui/chatwidget"
)

const windowsSandboxDisallowedNotice = "That Windows sandbox option is disallowed by requirements."

type WindowsSandboxSetupOutcome struct {
	Started    bool
	Completion *WindowsSandboxSetupCompletion
}

type WindowsSandboxSetupCompletion struct {
	Mode    chatwidget.WindowsSandboxMode
	Success bool
	Error   string
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
