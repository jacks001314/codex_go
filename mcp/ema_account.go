package mcp

import (
	"strings"

	"codex_go/auth"
	managedconfig "codex_go/config"
)

// MCPEMAAccountGrantIsUsable mirrors Rust account_grant_is_usable (#55698): a
// persisted Codex account grant may authenticate an enterprise MCP server only
// when its trusted OAuth provenance names the configured issuer, the record is
// complete, its ID token names the same account, and the ID-token issuer,
// audience, and expiry bindings are usable. The issuer is compared exactly as
// Rust does, so a trailing-slash difference stays a configuration problem
// instead of becoming a silent match.
func MCPEMAAccountGrantIsUsable(tokens map[string]any, idp managedconfig.MCPServerIdPOAuthConfig) bool {
	facts := auth.AccountTokenFactsFromTokens(tokens)
	issuer := strings.TrimSpace(idp.Issuer)
	if facts.Issuer == "" || facts.Issuer != issuer {
		return false
	}
	if facts.LoginOwnerID == "" || facts.RefreshToken == "" || facts.ChatGPTUserID == "" || facts.AccountID == "" {
		return false
	}
	// The ID token must describe the same account the record belongs to, so an
	// account grant can never be redirected to another workspace.
	if claimAccountID := strings.TrimSpace(auth.ChatGPTClaimsFromJWT(facts.IDToken).AccountID); claimAccountID != facts.AccountID {
		return false
	}
	return MCPEMAAccountOIDCIdentityIsUsable(facts.IDToken, facts.Issuer, facts.ClientID)
}

// MCPEMAAccountConfigurationIssue mirrors Rust account_configuration_issue: it
// explains why an eligible account grant is missing so the failure names a
// sign-in or an issuer configuration fix instead of a generic error. An empty
// result means there is no actionable diagnosis.
func MCPEMAAccountConfigurationIssue(tokens map[string]any, idp managedconfig.MCPServerIdPOAuthConfig) string {
	facts := auth.AccountTokenFactsFromTokens(tokens)
	issuer := strings.TrimSpace(idp.Issuer)
	if facts.LoginOwnerID == "" &&
		((facts.Issuer != "" && facts.Issuer == issuer) || MCPEMAAccountOIDCIssuerHint(facts.IDToken, idp.Issuer)) {
		return "Sign in to your Codex account again to enable this enterprise MCP server"
	}
	if facts.Issuer != "" && facts.Issuer != issuer &&
		strings.TrimRight(facts.Issuer, "/") == strings.TrimRight(issuer, "/") {
		return "Codex sign-in and enterprise MCP issuer strings differ by a trailing slash; align their OIDC issuer configuration"
	}
	return ""
}
