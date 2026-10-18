package tea

import (
	"fmt"
	"strings"

	"codex_go/features"
)

// cyberAccessProgramForTurn selects the explicit cyber program for a TUI turn
// (Rust daybreak::program_for_turn #49859): eligible accounts use the catalog's
// standard program with Daybreak off and daybreak_blue with it on.
func (m *Model) cyberAccessProgramForTurn() (string, error) {
	if m == nil || m.State == nil || !m.cyberAccessAccountEligible() {
		return "", nil
	}
	modelID := strings.TrimSpace(m.State.Model)
	programs := []string{}
	for _, option := range m.modelPickerOpts {
		if strings.EqualFold(strings.TrimSpace(option.ID), modelID) {
			programs = option.AvailableCyberPrograms
			break
		}
	}
	contains := func(want string) bool {
		for _, program := range programs {
			if strings.EqualFold(strings.TrimSpace(program), want) {
				return true
			}
		}
		return false
	}
	if m.daybreakEnabled {
		if contains("daybreak_blue") {
			return "daybreak_blue", nil
		}
		return "", fmt.Errorf("Daybreak is unavailable for model %s", modelID)
	}
	if contains("standard") {
		return "standard", nil
	}
	return "", nil
}

func (m *Model) cyberAccessAccountEligible() bool {
	if m == nil || m.State == nil {
		return false
	}
	provider := strings.TrimSpace(m.State.Provider)
	if provider != "" && !strings.EqualFold(provider, "openai") {
		return false
	}
	if m.hasChatGPTAccount || m.State.HasChatGPTAccount {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(m.State.AccountDisplay), "API key configured") {
		return features.Enabled(m.featureSettings, "api_key_cyber_access_programs")
	}
	return true
}
