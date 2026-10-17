package tea

import (
	"strings"

	"codex_go/features"
)

func daybreakFeatureEnabledForAccount(featureSettings map[string]bool, hasChatGPTAccount bool, accountDisplay string) bool {
	if !features.Enabled(featureSettings, "cli_daybreak") {
		return false
	}
	if hasChatGPTAccount {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(accountDisplay), "API key configured") {
		return features.Enabled(featureSettings, "api_key_cyber_access_programs")
	}
	// Preserve a resumed preference while account state is still unknown.
	return true
}

func (m *Model) daybreakFeatureEnabled() bool {
	if m == nil {
		return false
	}
	hasChatGPT := m.hasChatGPTAccount || (m.State != nil && m.State.HasChatGPTAccount)
	display := ""
	if m.State != nil {
		display = m.State.AccountDisplay
	}
	return daybreakFeatureEnabledForAccount(m.featureSettings, hasChatGPT, display)
}

func (m *Model) daybreakAccountEligible() bool {
	if m == nil || !m.daybreakFeatureEnabled() {
		return false
	}
	if m.State != nil {
		provider := strings.TrimSpace(m.State.Provider)
		if provider != "" && !strings.EqualFold(provider, "openai") {
			return false
		}
	}
	hasChatGPT := m.hasChatGPTAccount || (m.State != nil && m.State.HasChatGPTAccount)
	if hasChatGPT {
		return true
	}
	return m.State != nil && strings.HasPrefix(strings.TrimSpace(m.State.AccountDisplay), "API key configured")
}
