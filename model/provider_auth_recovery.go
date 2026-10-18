package model

import "strings"

// ProviderAuthRecoveryMessages carries the user-facing lifecycle text of a
// provider-owned recovery (Rust ProviderAuthRecoveryMessages, #41239).
type ProviderAuthRecoveryMessages struct {
	Started   string
	Succeeded string
}

// ProviderAuthRecoveryPhase identifies one app-visible recovery event.
type ProviderAuthRecoveryPhase string

const (
	ProviderAuthRecoveryPhaseStarted   ProviderAuthRecoveryPhase = "started"
	ProviderAuthRecoveryPhaseCompleted ProviderAuthRecoveryPhase = "completed"
)

// ProviderAuthRecoveryEvent is one provider-owned recovery progress event. The
// host turns it into the turn-scoped modelProvider/authRecoveryStarted or
// authRecoveryCompleted app-server notification.
type ProviderAuthRecoveryEvent struct {
	Phase    ProviderAuthRecoveryPhase
	Provider string
	Message  string
}

// ProviderAuthRecoveryHandler receives provider-owned recovery progress.
type ProviderAuthRecoveryHandler func(ProviderAuthRecoveryEvent)

// ProviderAuthRecoveryMessagesForInfo mirrors Rust
// AmazonBedrockModelProvider::auth_recovery_messages: AWS SDK/credential-export
// recovery is user-visible, while other providers have no lifecycle messages.
func ProviderAuthRecoveryMessagesForInfo(info ProviderInfo) *ProviderAuthRecoveryMessages {
	if !info.IsAmazonBedrock() || info.AWS == nil {
		return nil
	}
	if info.AWS.AuthRefresh == nil && info.AWS.CredentialExport == nil {
		return nil
	}
	return &ProviderAuthRecoveryMessages{
		Started:   "AWS session has expired. Reauthenticating...",
		Succeeded: "Signed in with AWS.",
	}
}

func (r *ResponsesAgentRunner) providerAuthRecoveryMessages() *ProviderAuthRecoveryMessages {
	if r == nil || r.Provider == nil {
		return nil
	}
	return ProviderAuthRecoveryMessagesForInfo(ProviderInfo{Name: r.Provider.Name, AWS: r.AWS})
}

func (r *ResponsesAgentRunner) emitProviderAuthRecovery(phase ProviderAuthRecoveryPhase, message string) {
	if r == nil || r.AuthRecoveryHandler == nil || strings.TrimSpace(message) == "" {
		return
	}
	provider := ""
	if r.Provider != nil {
		provider = strings.TrimSpace(r.Provider.Name)
	}
	if provider == "" {
		provider = strings.TrimSpace(r.ProviderID)
	}
	r.AuthRecoveryHandler(ProviderAuthRecoveryEvent{
		Phase:    phase,
		Provider: provider,
		Message:  message,
	})
}
