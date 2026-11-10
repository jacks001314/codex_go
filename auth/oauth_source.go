package auth

// OAuth source claims that decide whether two credentials belong to the same
// local credential source (Rust #60433, login/src/auth/source.rs). They are
// continuity hints for runtime reuse; the registration service still verifies
// the credential.

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// oauthSource is the comparable subset of an OAuth access token's claims.
type oauthSource struct {
	iss       string
	sessionID string
	sub       string
	clientID  string
	azp       string
	// identity is the canonical JSON of the "https://api.openai.com/auth"
	// claim, so a missing field and an empty field stay distinguishable.
	identity string
}

// oauthSourceFromToken parses one three-part JWT whose iss and session_id are
// both non-empty. Tokens without parseable source claims never imply continuity.
func oauthSourceFromToken(token string) (oauthSource, bool) {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return oauthSource{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return oauthSource{}, false
	}
	var claims struct {
		Iss       string         `json:"iss"`
		SessionID string         `json:"session_id"`
		Sub       string         `json:"sub"`
		ClientID  string         `json:"client_id"`
		AZP       string         `json:"azp"`
		Identity  map[string]any `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(decoded, &claims) != nil {
		return oauthSource{}, false
	}
	if strings.TrimSpace(claims.Iss) == "" || strings.TrimSpace(claims.SessionID) == "" {
		return oauthSource{}, false
	}
	identity := "null"
	if claims.Identity != nil {
		encoded, err := json.Marshal(claims.Identity)
		if err != nil {
			return oauthSource{}, false
		}
		identity = string(encoded)
	}
	return oauthSource{
		iss:       claims.Iss,
		sessionID: claims.SessionID,
		sub:       claims.Sub,
		clientID:  claims.ClientID,
		azp:       claims.AZP,
		identity:  identity,
	}, true
}

// hasOAuthSessionClaim reports whether an exchange token can be bound to a
// local source at all (Rust has_session_claim).
func hasOAuthSessionClaim(token string) bool {
	_, ok := oauthSourceFromToken(token)
	return ok
}
