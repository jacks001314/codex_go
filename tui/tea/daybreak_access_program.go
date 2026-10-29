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
	option, ok := m.currentModelCatalogOption(modelID)
	if !ok || len(option.AvailableCyberPrograms) == 0 {
		// Missing or empty discovery metadata preserves the server's automatic
		// treatment (Rust #53717). An unlisted explicit model ID is handled the
		// same way.
		return "", nil
	}
	programs := option.AvailableCyberPrograms
	contains := func(want string) bool {
		for _, program := range programs {
			if strings.EqualFold(strings.TrimSpace(program), want) {
				return true
			}
		}
		return false
	}
	if m.daybreakEnabled {
		if program := daybreakProgram(programs); program != "" {
			return program, nil
		}
		return "", fmt.Errorf("Daybreak support for model %s could not be confirmed by the connected server. Use /daybreak to turn it off, or choose a compatible model and server.", modelID)
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
