package appserver

import (
	"strings"

	"codex_go/model"
)

// authRecoveryEventHandler maps provider-owned recovery progress onto the
// turn-scoped app-server notifications introduced by Rust #41239.
func (r *RuntimeRouter) authRecoveryEventHandler(threadID, turnID string) model.ProviderAuthRecoveryHandler {
	if r == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return nil
	}
	return func(event model.ProviderAuthRecoveryEvent) {
		var method NotificationMethod
		switch event.Phase {
		case model.ProviderAuthRecoveryPhaseStarted:
			method = NotificationAuthRecoveryStarted
		case model.ProviderAuthRecoveryPhaseCompleted:
			method = NotificationAuthRecoveryCompleted
		default:
			return
		}
		r.notify(method, &AuthRecoveryNotification{
			ThreadID: strings.TrimSpace(threadID),
			TurnID:   strings.TrimSpace(turnID),
			Provider: strings.TrimSpace(event.Provider),
			Message:  strings.TrimSpace(event.Message),
		})
	}
}
