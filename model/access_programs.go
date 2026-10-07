package model

import (
	"strings"

	"codex_go/auth"
	"codex_go/codexapi"
	"codex_go/features"
)

// AccessPrograms mirrors Rust `codex_api::AccessPrograms` (#44893): the explicit
// per-request access selection on the Responses API wire. Only the Cyber program
// exists today, and its value is the core snake_case program name.
type AccessPrograms struct {
	Cyber string `json:"cyber"`
}

// ApiKeyCyberAccessPrograms mirrors Rust
// `core::cyber_access_program::ApiKeyCyberAccessPrograms`: the config-derived
// policy for an explicit program on an API-key session.
//
// Rust resolves it in `from_config` from the turn's provider id plus the
// `api_key_cyber_access_programs` feature. Rust #49714 decoupled it from
// `api_key_model_discovery`, so model discovery no longer takes part. The zero
// value keeps the pre-#49714 Go behavior (drop the program, never fail) for a
// runner that was never handed a resolved policy.
type ApiKeyCyberAccessPrograms int

const (
	// ApiKeyCyberAccessProgramsUnsupportedProvider is the policy for every
	// provider other than the OpenAI provider: the program is dropped.
	ApiKeyCyberAccessProgramsUnsupportedProvider ApiKeyCyberAccessPrograms = iota
	// ApiKeyCyberAccessProgramsDisabled is the policy while the
	// `api_key_cyber_access_programs` feature is off: an API-key session that
	// selected a program fails its turn with an invalid-request error.
	ApiKeyCyberAccessProgramsDisabled
	// ApiKeyCyberAccessProgramsEnabled forwards an explicitly selected program
	// from an API-key session.
	ApiKeyCyberAccessProgramsEnabled
)

// ApiKeyCyberAccessProgramsFromConfig mirrors Rust
// `ApiKeyCyberAccessPrograms::from_config` (#49714): only the OpenAI provider id
// and the `api_key_cyber_access_programs` feature decide the policy, so enabling
// the forwarding feature is enough on its own.
func ApiKeyCyberAccessProgramsFromConfig(featureSettings map[string]bool, providerID string) ApiKeyCyberAccessPrograms {
	if strings.TrimSpace(providerID) != OpenAIProviderID {
		return ApiKeyCyberAccessProgramsUnsupportedProvider
	}
	if features.Enabled(featureSettings, "api_key_cyber_access_programs") {
		return ApiKeyCyberAccessProgramsEnabled
	}
	return ApiKeyCyberAccessProgramsDisabled
}

// AccessProgramsForAuth mirrors Rust `core::cyber_access_program::for_auth`: an
// explicit per-turn program reaches the request for an authenticated human
// ChatGPT account (Rust narrows the program to the OpenAI provider first, in
// `for_provider`), and an API-key session forwards it only while `policy`
// allows. Bedrock, Agent Identity and missing credentials never send it.
//
// Rust's ChatGPT gate is `CodexAuth::is_chatgpt_auth`, i.e.
// `AuthMode::has_chatgpt_account` (`chatgpt`, `chatgptAuthTokens`,
// `personalAccessToken`); its API-key gate is `CodexAuth::is_api_key_auth`, i.e.
// exactly `AuthMode::ApiKey`.
func AccessProgramsForAuth(program string, snapshot *auth.AuthDotJSON, policy ApiKeyCyberAccessPrograms) (*AccessPrograms, error) {
	trimmed := strings.TrimSpace(program)
	switch CyberAccessProgram(trimmed) {
	case CyberAccessProgramStandard, CyberAccessProgramDaybreakBlue, CyberAccessProgramDaybreakRed:
	default:
		return nil, nil
	}
	if snapshot == nil {
		return nil, nil
	}
	if authHasChatGPTAccountForProgram(snapshot) {
		return &AccessPrograms{Cyber: trimmed}, nil
	}
	if !authIsAPIKeyForProgram(snapshot) {
		return nil, nil
	}
	switch policy {
	case ApiKeyCyberAccessProgramsUnsupportedProvider:
		return nil, nil
	case ApiKeyCyberAccessProgramsDisabled:
		// Rust `for_auth` fails an API-key turn that selected a program while
		// forwarding is off, with this exact message.
		return nil, &codexapi.APIError{
			Kind:    codexapi.ErrorInvalidRequest,
			Message: "Cyber access programs are disabled for this API-key session.",
		}
	default:
		return &AccessPrograms{Cyber: trimmed}, nil
	}
}

// authIsAPIKeyForProgram reports whether the credential is an OpenAI API key
// (Rust `CodexAuth::is_api_key_auth` = `AuthMode::ApiKey`; Bedrock and Agent
// Identity credentials have their own modes and do not qualify).
func authIsAPIKeyForProgram(snapshot *auth.AuthDotJSON) bool {
	return snapshot != nil && snapshot.Mode() == "api-key"
}

// authHasChatGPTAccountForProgram reports whether the auth snapshot is an
// authenticated human ChatGPT account (Rust `AuthMode::has_chatgpt_account`).
func authHasChatGPTAccountForProgram(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token":
		return true
	default:
		return false
	}
}
