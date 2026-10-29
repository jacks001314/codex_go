package tea

import (
	"os"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
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
	previousCWD := strings.TrimSpace(m.sessionCWD)
	m.applyThreadSettingsValues(msg.Settings)
	// Rust #55682: a covering settings update settles the permissions hold so
	// queued prompts resume once the confirmed profile is in place.
	cmd := m.settlePermissionPopup()
	if !m.hasChatGPTAccount && strings.TrimSpace(m.sessionCWD) != previousCWD {
		// Rust #55682: the first attachment can change the project credentials,
		// so an API-key model catalog from the launch directory must not carry
		// over. Refresh the hidden-inclusive catalog for the attached cwd.
		cmd = bubbletea.Batch(cmd, m.fetchModelCatalog())
	}
	return cmd
}

// applyThreadSettingsValues applies one settings snapshot to the TUI state
// (shared by the settings notification and the resume response, Rust
// #43330/#43340).
// applyReportedCollaborationMode adopts the server's collaboration mode kind
// (Rust #45519 restores `session.collaboration_mode` from the resume response
// and applies it before the first turn of the resumed thread).
func (m *Model) applyReportedCollaborationMode(mode map[string]any) {
	if m == nil || m.State == nil {
		return
	}
	switch strings.ToLower(metadataString(mode, "mode")) {
	case string(chatwidget.CollaborationModeKindPlan):
		m.State.PlanMode = true
	case string(chatwidget.CollaborationModeKindDefault):
		m.State.PlanMode = false
	}
}

func (m *Model) applyThreadSettingsValues(settings appserver.Settings) {
	if m == nil || m.State == nil {
		return
	}
	cwdChanged := false
	if cwd := strings.TrimSpace(settings.CWD); cwd != "" && cwd != strings.TrimSpace(m.State.CWD) {
		m.State.CWD = cwd
		m.sessionCWD = cwd
		cwdChanged = true
		if m.onReadWindowsSandboxState != nil {
			// Rust #45821: Windows sandbox requirements are directory-scoped.
			m.windowsSandboxRefreshPending = true
		}
	}
	if settings.InstructionSourcesSet || settings.InstructionSources != nil {
		home, _ := os.UserHomeDir()
		m.State.AgentsSummary = codextui.FormatInstructionSources(settings.InstructionSources, m.State.CWD, home)
	}
	// Rust #46494: the server's settings carry the thread's runtime workspace
	// roots. Track them so a Session-sourced fork (a side conversation) can
	// forward the server-resolved roots rather than the client's config paths.
	m.sessionWorkspaceRoots = append([]string(nil), settings.RuntimeWorkspaceRoots...)
	if model := strings.TrimSpace(settings.Model); model != "" {
		m.State.Model = model
		m.clearRateLimitRecoveryHold()
	}
	if provider := strings.TrimSpace(settings.ModelProvider); provider != "" {
		m.State.Provider = provider
	}
	// Rust #45519: the resumed thread's server-reported collaboration mode is
	// authoritative, so the first prompt after a resume keeps the saved Plan
	// mode. Older servers omit it, and the local selection then stands.
	if settings.CollaborationMode != nil {
		m.applyReportedCollaborationMode(settings.CollaborationMode)
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
	// Rust #46008: the server-confirmed selection also refreshes the approvals
	// reviewer so a built-in preset adopts the authoritative setting.
	if reviewer := strings.TrimSpace(settings.ApprovalsReviewer); reviewer != "" {
		m.approvalsReviewer = chatwidget.ApprovalsReviewer(reviewer)
	}
	if settings.ActivePermissionProfile != nil {
		if profile := strings.TrimSpace(*settings.ActivePermissionProfile); profile != "" {
			m.State.Sandbox = profile
		}
	} else if sandbox := strings.TrimSpace(settings.SandboxPolicy); sandbox != "" {
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
