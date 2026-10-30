package auth

import (
	"bytes"
	"encoding/json"
	"strings"

	"codex_go/agent"
)

// knownStoredAuthModes mirrors the values Rust's AuthMode enum accepts while
// deserializing auth.json (serde rename_all = "lowercase" plus the explicit
// renames). Anything else is a data error there, so Go must not adopt it either
// (Rust #58324).
var knownStoredAuthModes = map[string]struct{}{
	"api-key":               {},
	"chatgpt":               {},
	"chatgptAuthTokens":     {},
	"headers":               {},
	"agent-identity":        {},
	"personal-access-token": {},
	"bedrock-api-key":       {},
	"bedrock-access-keys":   {},
}

// decodeStoredAuthDotJSON parses a persisted credential payload the way serde
// deserializes AuthDotJson: an unknown auth_mode is a data error that keeps its
// position but never the rejected value.
func decodeStoredAuthDotJSON(context string, data []byte) (*AuthDotJSON, error) {
	var auth AuthDotJSON
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, credentialStorageJSONContext(context, data, err)
	}
	if mode := strings.TrimSpace(auth.AuthMode); mode != "" {
		if _, ok := knownStoredAuthModes[normalizedAuthMode(mode)]; !ok {
			return nil, credentialStorageJSONContext(context, data, &credentialJSONDataError{offset: authModeValueOffset(data)})
		}
	}
	return &auth, nil
}

// authModeValueOffset locates the end of the top-level auth_mode value so a
// redacted error can report the line/column shape serde_json uses. It reports 0
// when the document no longer scans cleanly, which jsonErrorPosition clamps.
func authModeValueOffset(data []byte) int64 {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return 0
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return 0
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return 0
		}
		key, _ := keyToken.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return 0
		}
		if key == "auth_mode" {
			return decoder.InputOffset()
		}
	}
	return 0
}

// ValidateStoredAuthCredential mirrors the credential checks Rust runs when it
// turns a stored AuthDotJson into live auth: agent identity JWT material must
// decode. Decode failures carry credential-safe context and stay transient for
// unauthorized recovery (Rust #58324).
func ValidateStoredAuthCredential(snapshot *AuthDotJSON) error {
	if snapshot == nil || snapshot.Mode() != "agent-identity" {
		return nil
	}
	jwt, ok := snapshot.AgentIdentity.(string)
	if !ok {
		return nil
	}
	jwt = strings.TrimSpace(jwt)
	if jwt == "" {
		return nil
	}
	if _, err := agent.DecodeAgentIdentityJWT(jwt, nil); err != nil {
		return credentialStorageContext("failed to decode agent identity JWT", err)
	}
	return nil
}
