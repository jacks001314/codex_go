package mcp

import "strings"

// OAuthCredentialsStoreMode mirrors Rust
// codex_config::types::OAuthCredentialsStoreMode for `mcp_oauth_credentials_store`.
type OAuthCredentialsStoreMode string

const (
	// OAuthCredentialsStoreAuto is Rust's default: prefer the keyring and fall
	// back to the credentials file when keyring storage is unavailable.
	OAuthCredentialsStoreAuto OAuthCredentialsStoreMode = "auto"
	// OAuthCredentialsStoreFile stores credentials in
	// CODEX_HOME/.credentials.json.
	OAuthCredentialsStoreFile OAuthCredentialsStoreMode = "file"
	// OAuthCredentialsStoreKeyring requires keyring storage and fails when it is
	// unavailable (Rust: "Keyring when available, otherwise fail").
	OAuthCredentialsStoreKeyring OAuthCredentialsStoreMode = "keyring"
)

// ParseOAuthCredentialsStoreMode parses a configured mode, mirroring Rust's
// serde enum: an unrecognized value is invalid.
func ParseOAuthCredentialsStoreMode(value string) (OAuthCredentialsStoreMode, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(OAuthCredentialsStoreAuto):
		return OAuthCredentialsStoreAuto, true
	case string(OAuthCredentialsStoreFile):
		return OAuthCredentialsStoreFile, true
	case string(OAuthCredentialsStoreKeyring):
		return OAuthCredentialsStoreKeyring, true
	default:
		return "", false
	}
}

// MCPOAuthKeyringAvailable reports whether an OS keyring backend is available
// for MCP OAuth credentials.
//
// Go does not link an OS keyring (the `auth` package's KeyringStore is an
// in-process emulation), so the keyring is never a durable credential source
// here. Rust's `auto` therefore resolves to the credentials file - Rust's own
// documented fallback when keyring storage is unavailable - and `keyring`
// fails like Rust does when the keyring cannot be used.
const MCPOAuthKeyringAvailable = false

// MCPOAuthKeyringUnavailableError reports that keyring credential storage was
// requested but no keyring backend is available.
const MCPOAuthKeyringUnavailableError = "MCP OAuth keyring storage is unavailable; set mcp_oauth_credentials_store = \"file\" or \"auto\""

// runtimeOAuthCredentialsStoreMode reads the global
// `mcp_oauth_credentials_store` value; an invalid mode falls back to auto here
// and is rejected during config load (Rust's enum deserialization).
func runtimeOAuthCredentialsStoreMode(values map[string]any) OAuthCredentialsStoreMode {
	if values == nil {
		return OAuthCredentialsStoreAuto
	}
	for _, key := range []string{"mcp_oauth_credentials_store", "mcpOauthCredentialsStore"} {
		raw, ok := values[key].(string)
		if !ok {
			continue
		}
		if mode, valid := ParseOAuthCredentialsStoreMode(raw); valid {
			return mode
		}
	}
	return OAuthCredentialsStoreAuto
}
