package mcp

import (
	"strings"
	"sync"

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

// EMAState remembers rejected account grants per enterprise registration
// across threads. It retains only a revision digest, never a credential, so a
// rejection cannot outlive the grant it describes (Rust EmaState, #55859).
type EMAState struct {
	mu       sync.Mutex
	rejected []emaRejectedGrant
}

type emaRejectedGrant struct {
	registration emaRegistrationIdentity
	revision     auth.AccountOAuthGrantRevision
}

// NewEMAState returns an empty enterprise account-grant state.
func NewEMAState() *EMAState {
	return &EMAState{}
}

// RequiresSignIn reports whether the registration must sign the account in
// again before this exact grant revision may be reused.
func (s *EMAState) RequiresSignIn(registration emaRegistrationIdentity, tokens map[string]any) bool {
	if s == nil {
		return false
	}
	revision := auth.AccountOAuthGrantRevisionFromTokens(tokens)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rejected := range s.rejected {
		if rejected.registration == registration && rejected.revision == revision {
			return true
		}
	}
	return false
}

// Reject records that the registration rejected this grant revision. Replacing
// a registration's marker also bounds repeated refresh/rejection cycles.
func (s *EMAState) Reject(registration emaRegistrationIdentity, tokens map[string]any) {
	if s == nil {
		return
	}
	revision := auth.AccountOAuthGrantRevisionFromTokens(tokens)
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.rejected[:0]
	for _, rejected := range s.rejected {
		if rejected.registration != registration {
			kept = append(kept, rejected)
		}
	}
	s.rejected = append(kept, emaRejectedGrant{registration: registration, revision: revision})
}

// AccountIsUsable reports whether an eligible, not-yet-rejected account grant
// can authenticate this registration.
func (s *EMAState) AccountIsUsable(tokens map[string]any, registration emaRegistrationIdentity, idp managedconfig.MCPServerIdPOAuthConfig) bool {
	return MCPEMAAccountGrantIsUsable(tokens, idp) && !s.RequiresSignIn(registration, tokens)
}

// emaRegistrationIdentity identifies one enterprise MCP registration for
// rejection markers, mirroring Rust McpEmaRegistration equality: the resource
// registration plus the IdP it trusts.
type emaRegistrationIdentity struct {
	serverURL                 string
	resource                  string
	clientID                  string
	authorizationServerIssuer string
	scopes                    string
	idpIssuer                 string
	idpClientID               string
}

// registrationIdentity returns the comparable identity of this manager's
// enterprise registration.
func (m *EMAAuthManager) registrationIdentity() emaRegistrationIdentity {
	if m == nil {
		return emaRegistrationIdentity{}
	}
	return emaRegistrationIdentity{
		serverURL:                 m.serverURL,
		resource:                  m.resource,
		clientID:                  m.clientID,
		authorizationServerIssuer: m.authorizationServerIssuer,
		scopes:                    strings.Join(m.scopes, " "),
		idpIssuer:                 m.profile.IDP.Issuer,
		idpClientID:               m.profile.IDP.ClientID,
	}
}
