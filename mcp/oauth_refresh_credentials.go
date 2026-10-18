package mcp

import (
	"errors"
	"strings"
)

// mcpOAuthRefreshCredentials selects the client identity for a token refresh.
// A configured pre-registered client is authoritative; a stored credential with
// a different client ID must log in again before contacting the provider. A
// configured secret is used for the request but omitted from the persisted token
// record (Rust #47891).
func mcpOAuthRefreshCredentials(config *ServerConfig, stored *OAuthTokenSet) (string, string, bool, error) {
	storedClientID := ""
	storedClientSecret := ""
	if stored != nil {
		storedClientID = strings.TrimSpace(stored.ClientID)
		storedClientSecret = strings.TrimSpace(stored.ClientSecret)
	}
	configuredClientID := ""
	configuredClientSecret := ""
	if config != nil {
		configuredClientID = strings.TrimSpace(config.OAuthClientID)
		configuredClientSecret = strings.TrimSpace(config.OAuthClientSecret)
	}
	clientID := storedClientID
	if configuredClientID != "" {
		if storedClientID != "" && storedClientID != configuredClientID {
			return "", "", false, errors.New("MCP OAuth reauthentication required: configured client ID changed")
		}
		clientID = configuredClientID
	}
	if configuredClientSecret != "" {
		return clientID, configuredClientSecret, true, nil
	}
	return clientID, storedClientSecret, false, nil
}
