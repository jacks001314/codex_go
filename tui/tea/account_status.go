package tea

import (
	"strings"

	tuistatus "codex_go/tui/status"
)

// applyAccountStatusUpdated replaces the status account immediately. The email
// request id identifies the single asynchronous result allowed to complete it
// (Rust #50199).
func (m *Model) applyAccountStatusUpdated(message AccountStatusUpdatedMsg) {
	if m == nil {
		return
	}
	if m.State != nil {
		m.State.AccountDisplay = strings.TrimSpace(message.Display)
		m.State.HasChatGPTAccount = message.HasChatGPTAccount
	}
	m.hasChatGPTAccount = message.HasChatGPTAccount
	m.accountEmailRequestID = strings.TrimSpace(message.EmailRequestID)
	m.refreshTranscript()
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
