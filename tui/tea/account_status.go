package tea

import (
	"strings"

	"codex_go/tui/chatwidget"
	tuistatus "codex_go/tui/status"
)

// applyAccountStatusUpdated replaces the status account immediately. The email
// request id identifies the single asynchronous result allowed to complete it
// (Rust #50199).
func (m *Model) applyAccountStatusUpdated(message AccountStatusUpdatedMsg) {
	if m == nil {
		return
	}
	previousDisplay := ""
	previousChatGPT := m.hasChatGPTAccount
	if m.State != nil {
		previousDisplay = strings.TrimSpace(m.State.AccountDisplay)
		previousChatGPT = m.hasChatGPTAccount || m.State.HasChatGPTAccount
	}
	if m.State != nil {
		m.State.AccountDisplay = strings.TrimSpace(message.Display)
		m.State.HasChatGPTAccount = message.HasChatGPTAccount
	}
	m.hasChatGPTAccount = message.HasChatGPTAccount
	if accountIdentityChanged(previousDisplay, m.State.AccountDisplay, previousChatGPT, message.HasChatGPTAccount) {
		// Rust update_account_state clears the usage notice state when the
		// account changes; quota and warning thresholds belong to the old
		// account and must not carry into the new one.
		m.clearUsageNoticeState()
	}
	m.accountEmailRequestID = strings.TrimSpace(message.EmailRequestID)
	m.refreshTranscript()
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
	m.chatGPTPlanType = ""
	m.clearThreadUsage()
	m.syncStateRateLimits()
}

// applyAccountEmailLoaded updates only the current account-email request and
// reports whether the display changed.
func (m *Model) applyAccountEmailLoaded(message AccountEmailLoadedMsg) bool {
	if m == nil || m.State == nil {
		return false
	}
	requestID := strings.TrimSpace(message.RequestID)
	if requestID == "" || requestID != m.accountEmailRequestID {
		return false
	}
	m.accountEmailRequestID = ""
	m.State.AccountDisplay = tuistatus.AccountDisplayWithEmail(m.State.AccountDisplay, message.Email)
	m.refreshTranscript()
	return true
}
