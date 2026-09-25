package model

import (
	"strings"

	"codex_go/auth"
)

// AccessPrograms mirrors Rust `codex_api::AccessPrograms` (#44893): the explicit
// per-request access selection on the Responses API wire. Only the Cyber program
// exists today, and its value is the core snake_case program name.
type AccessPrograms struct {
	Cyber string `json:"cyber"`
}

// AccessProgramsForAuth mirrors Rust `core::cyber_access_program::for_auth`: an
// explicit per-turn program reaches the request only for an authenticated human
// ChatGPT account, and no program preserves the backend's automatic behavior.
//
// Rust's gate is `CodexAuth::is_chatgpt_auth`, i.e.
// `AuthMode::has_chatgpt_account` (`chatgpt`, `chatgptAuthTokens`,
// `personalAccessToken`). An Agent Identity credential uses the Codex backend
// but is not a ChatGPT account, so it does not qualify, and neither do API-key
// or Bedrock credential modes.
func AccessProgramsForAuth(program string, snapshot *auth.AuthDotJSON) *AccessPrograms {
	switch CyberAccessProgram(strings.TrimSpace(program)) {
	case CyberAccessProgramStandard, CyberAccessProgramDaybreakBlue, CyberAccessProgramDaybreakRed:
	default:
		return nil
	}
	if !authHasChatGPTAccountForProgram(snapshot) {
		return nil
	}
	return &AccessPrograms{Cyber: strings.TrimSpace(program)}
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
