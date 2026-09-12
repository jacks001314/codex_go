package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
)

// ThreadSettingsUpdatedMsg carries the app server's authoritative settings for
// one thread (Rust #43330/#43340: chatwidget on_thread_settings_updated).
type ThreadSettingsUpdatedMsg struct {
	ThreadID string
	Settings appserver.Settings
}

// applyThreadSettingsUpdated applies the active thread's settings to the TUI
// state so restored or updated permissions, model, cwd, and provider come from
// the server rather than the previous task. Updates for other threads are
// ignored, matching Rust's thread routing.
func (m *Model) applyThreadSettingsUpdated(msg ThreadSettingsUpdatedMsg) bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	threadID := strings.TrimSpace(msg.ThreadID)
	if threadID == "" || threadID != strings.TrimSpace(m.State.ThreadID) {
		return nil
	}
	// Rust #43340: the server confirming the thread's settings completes any
	// pending named-profile selection.
	m.pendingServerProfile = ""
	m.applyThreadSettingsValues(msg.Settings)
	return nil
}

// applyThreadSettingsValues applies one settings snapshot to the TUI state
// (shared by the settings notification and the resume response, Rust
// #43330/#43340).
func (m *Model) applyThreadSettingsValues(settings appserver.Settings) {
	if m == nil || m.State == nil {
		return
	}
	cwdChanged := false
	if cwd := strings.TrimSpace(settings.CWD); cwd != "" && cwd != strings.TrimSpace(m.State.CWD) {
		m.State.CWD = cwd
		m.sessionCWD = cwd
		cwdChanged = true
	}
	if model := strings.TrimSpace(settings.Model); model != "" {
		m.State.Model = model
	}
	if provider := strings.TrimSpace(settings.ModelProvider); provider != "" {
		m.State.Provider = provider
	}
	// The server's optional settings clear the local override when absent
	// (Rust assigns the Option directly).
	effort := ""
	if settings.Effort != nil {
		effort = strings.TrimSpace(*settings.Effort)
	}
	if m.State.PlanMode {
		m.State.PlanModeReasoningEffort = effort
	} else {
		m.State.ReasoningEffort = effort
	}
	serviceTier := ""
	if settings.ServiceTier != nil {
		serviceTier = strings.TrimSpace(*settings.ServiceTier)
	}
	m.State.ServiceTier = serviceTier
	if policy := strings.TrimSpace(settings.ApprovalPolicy); policy != "" {
		m.State.ApprovalPolicy = policy
	}
	if sandbox := strings.TrimSpace(settings.SandboxPolicy); sandbox != "" {
		m.State.Sandbox = sandbox
	}
	personality := ""
	if settings.Personality != nil {
		personality = strings.TrimSpace(*settings.Personality)
	}
	m.State.Personality = personality

	m.refreshServiceTierCommands()
	if cwdChanged {
		m.invalidateAppsScope()
	}
	m.refreshTranscript()
}
