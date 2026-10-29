package app

// Rust parity: codex-rs/tui/src/dynamic_tools.rs background_turn_program plus
// the Daybreak config inheritance in create_thread (#53701/#53717).

import (
	"context"
	"fmt"
	"strings"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/config"
	"codex_go/features"
	modelpkg "codex_go/model"
	"codex_go/turn"
)

// tuiFeatureSettings resolves the TUI's own feature gate. Rust passes the
// TUI's resolved Features into dynamic_tools::execute; a host that cannot
// resolve them skips the Daybreak override instead of guessing.
func (e *dynamicToolExecutor) tuiFeatureSettings() (map[string]bool, bool) {
	if e != nil && e.options.FeatureSettings != nil {
		return e.options.FeatureSettings, true
	}
	if e == nil || e.client == nil || e.client.root == nil {
		return nil, false
	}
	settings, err := interactiveLoadSettings(e.client.root)
	if err != nil {
		return nil, false
	}
	return settings.FeatureSettings, true
}

// effectiveDaybreakDefault reads the effective `daybreak` preference for the
// source thread's working directory, which is how a background task inherits
// project-local opt-outs (Rust read_effective_config_if_supported). A server
// that cannot report the configuration keeps the omission.
func (e *dynamicToolExecutor) effectiveDaybreakDefault(cwd string) *bool {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	var read config.ConfigReadResponse
	params := config.ConfigReadParams{IncludeLayers: true, CWD: &cwd}
	if err := e.request(appserver.MethodConfigRead, params, &read); err != nil {
		return nil
	}
	value, ok := read.Config["daybreak"].(bool)
	if !ok {
		return nil
	}
	return &value
}

// backgroundTurnProgram mirrors Rust background_turn_program: a background
// turn keeps the target thread's saved Daybreak choice only for accounts that
// can use it. API-key accounts additionally require the target thread's
// `api_key_cyber_access_programs` setting, so project opt-outs are honored.
// Server-side discovery gaps omit the override.
func (e *dynamicToolExecutor) backgroundTurnProgram(threadID string, modelID string, providerID string, daybreakEnabled bool) (string, error) {
	if e == nil {
		return "", nil
	}
	featureSettings, ok := e.tuiFeatureSettings()
	if !ok || !features.Enabled(featureSettings, "cli_daybreak") {
		return "", nil
	}
	var accountResponse auth.GetAccountResponse
	accountErr := e.request(appserver.MethodGetAccount, auth.GetAccountParams{}, &accountResponse)
	accountType := auth.AccountType("")
	if accountErr == nil && accountResponse.Account != nil {
		accountType = accountResponse.Account.Type
	}
	if accountErr != nil && daybreakEnabled && providerID == modelpkg.OpenAIProviderID {
		return "", fmt.Errorf("Could not determine Daybreak account eligibility: %v", accountErr)
	}
	eligible := false
	if providerID == modelpkg.OpenAIProviderID {
		switch accountType {
		case auth.AccountChatGPT:
			eligible = true
		case auth.AccountAPIKey:
			entries, err := fetchExperimentalFeatures(e.ctx, func(_ context.Context, method appserver.Method, params any, target any) error {
				return e.request(method, params, target)
			}, threadID)
			if err != nil {
				// Older servers that cannot report the target thread's
				// settings keep the omission.
				return "", nil
			}
			for _, entry := range entries {
				if entry.Name == "api_key_cyber_access_programs" && entry.Enabled {
					eligible = true
					break
				}
			}
			if !eligible {
				return "", nil
			}
		}
	}
	enabled := daybreakEnabled && eligible
	programs := e.backgroundCyberPrograms(modelID)
	if len(programs) == 0 {
		// Missing or empty discovery metadata preserves the server's automatic
		// treatment.
		return "", nil
	}
	if enabled {
		if program := backgroundDaybreakProgram(programs); program != "" {
			return program, nil
		}
		return "", fmt.Errorf("Daybreak support for model %s could not be confirmed by the connected server. Use /daybreak to turn it off, or choose a compatible model and server.", modelID)
	}
	if !eligible {
		return "", nil
	}
	for _, program := range programs {
		if program == string(modelpkg.CyberAccessProgramStandard) {
			return program, nil
		}
	}
	return "", nil
}

// backgroundCyberPrograms lists one model's advertised cyber programs from the
// target app server (Rust ModelList include_hidden).
func (e *dynamicToolExecutor) backgroundCyberPrograms(modelID string) []string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	includeHidden := true
	var listed modelpkg.ModelListResponse
	if err := e.request(appserver.MethodModelList, modelpkg.ModelListParams{IncludeHidden: &includeHidden}, &listed); err != nil {
		return nil
	}
	entries := listed.Data
	if len(entries) == 0 {
		entries = listed.Models
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Model) != modelID || entry.AvailableAccessPrograms == nil {
			continue
		}
		programs := make([]string, 0, len(entry.AvailableAccessPrograms.Cyber))
		for _, name := range entry.AvailableAccessPrograms.Cyber {
			if core := turn.CyberAccessProgram(name).CoreValue(); core != "" {
				programs = append(programs, core)
			}
		}
		return programs
	}
	return nil
}

// backgroundDaybreakProgram prefers Daybreak Blue over Red (Rust
// ModelAccessPrograms::daybreak).
func backgroundDaybreakProgram(programs []string) string {
	for _, want := range []string{string(modelpkg.CyberAccessProgramDaybreakBlue), string(modelpkg.CyberAccessProgramDaybreakRed)} {
		for _, program := range programs {
			if program == want {
				return program
			}
		}
	}
	return ""
}

// wireCyberAccessProgram maps a core snake_case program onto the app-server
// turn/start wire value, dropping anything unknown.
func wireCyberAccessProgram(program string) *turn.CyberAccessProgram {
	switch strings.TrimSpace(program) {
	case string(modelpkg.CyberAccessProgramStandard):
		value := turn.CyberAccessProgramStandard
		return &value
	case string(modelpkg.CyberAccessProgramDaybreakBlue):
		value := turn.CyberAccessProgramDaybreakBlue
		return &value
	case string(modelpkg.CyberAccessProgramDaybreakRed):
		value := turn.CyberAccessProgramDaybreakRed
		return &value
	default:
		return nil
	}
}
