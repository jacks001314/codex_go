package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/tui/chatwidget"
	tuistatus "codex_go/tui/status"
)

// applyAccountStatusUpdated replaces the status account immediately. The email
// request id identifies the single asynchronous result allowed to complete it
// (Rust #50199).
func (m *Model) applyAccountStatusUpdated(message AccountStatusUpdatedMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	previousDisplay := ""
	previousChatGPT := m.hasChatGPTAccount
	if m.State != nil {
		previousDisplay = strings.TrimSpace(m.State.AccountDisplay)
		previousChatGPT = m.hasChatGPTAccount || m.State.HasChatGPTAccount
	}
	if m.State != nil {
		m.State.AccountDisplay = strings.TrimSpace(message.Display)
		m.State.WorkspaceDisplay = strings.TrimSpace(message.Workspace)
		m.State.HasChatGPTAccount = message.HasChatGPTAccount
	}
	m.hasChatGPTAccount = message.HasChatGPTAccount
	if accountIdentityChanged(previousDisplay, m.State.AccountDisplay, previousChatGPT, message.HasChatGPTAccount) {
		// Rust update_account_state clears the usage notice state when the
		// account changes; quota and warning thresholds belong to the old
		// account and must not carry into the new one.
		m.clearUsageNoticeState()
		// Rust #50140 also drops the cached server permission catalog, which is
		// scoped to the account the connected server authenticated.
		m.invalidatePermissionDiscoveryCache()
		// Rust #63553 clears the cached model catalogs on an account change;
		// each provider list belongs to the account the server authenticated.
		m.clearModelPickerCatalogs()
	}
	// Rust update_account_state always rebinds the account plan and drops the
	// quota plan, which spoke for the previous account (#61978).
	planCmd := m.setAccountPlan(message.PlanType)
	m.accountEmailRequestID = strings.TrimSpace(message.EmailRequestID)
	m.refreshTranscript()
	return planCmd
}

func accountIdentityChanged(previousDisplay, currentDisplay string, previousChatGPT, currentChatGPT bool) bool {
	if previousChatGPT != currentChatGPT {
		return true
	}
	return previousDisplay != "" && currentDisplay != "" && previousDisplay != currentDisplay
}

func (m *Model) clearUsageNoticeState() {
	if m == nil {
		return
	}
	m.rateLimitSnapshots = map[string]chatwidget.RateLimitSnapshot{}
	m.rateLimitWarnings = chatwidget.RateLimitWarningState{}
	m.quotaPlanType = ""
	m.clearThreadUsage()
	m.syncStateRateLimits()
}

// applyAccountEmailLoaded updates only the current account-email request and
// reports whether the display changed.
func (m *Model) applyAccountEmailLoaded(message AccountEmailLoadedMsg) (bool, bubbletea.Cmd) {
	if m == nil || m.State == nil {
		return false, nil
	}
	requestID := strings.TrimSpace(message.RequestID)
	if requestID == "" || requestID != m.accountEmailRequestID {
		return false, nil
	}
	m.accountEmailRequestID = ""
	m.State.AccountDisplay = tuistatus.AccountDisplayWithEmail(m.State.AccountDisplay, message.Email)
	m.State.WorkspaceDisplay = strings.TrimSpace(message.Workspace)
	// Rust on_account_email_loaded rebinds the account plan from the fresh read
	// while keeping the quota plan, so a known quota plan still wins.
	planCmd := m.setAccountReadPlan(message.PlanType)
	m.refreshTranscript()
	return true, planCmd
}
