package mcp

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// MCPOAuthCallbackMode is the OAuth mix-up defense associated with a registered
// callback. Mirrors Rust McpOAuthCallbackMode (rmcp-client oauth_callback.rs).
//
// Codex can authorize against many independent MCP servers. If those servers
// share a callback URL and a response does not identify its authorization
// server, a code could be associated with the wrong server and sent to an
// attacker-controlled token endpoint (RFC 9700 authorization-server mix-up).
type MCPOAuthCallbackMode string

const (
	// MCPOAuthCallbackSpecific appends an ID derived from the complete MCP
	// server URL so distinct callback paths bind each response to its server.
	MCPOAuthCallbackSpecific MCPOAuthCallbackMode = "callback_specific"
	// MCPOAuthCallbackIssuerBound reuses a stable callback and relies on the
	// validated authorization-response `iss` parameter instead.
	MCPOAuthCallbackIssuerBound MCPOAuthCallbackMode = "issuer_bound"
)

// mcpOAuthDefaultCallbackURL mirrors Rust's fallback registered callback.
const mcpOAuthDefaultCallbackURL = "http://127.0.0.1/callback"

// mcpOAuthOptionalString returns a pointer to a non-empty trimmed value.
func mcpOAuthOptionalString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// MCPOAuthCallbackModeForDiscovery mirrors Rust callback_mode(metadata):
// issuer binding is used only when the authorization server advertises
// authorization-response issuer support and publishes a metadata issuer.
func MCPOAuthCallbackModeForDiscovery(issuerResponseSupported bool, metadataIssuer string) (MCPOAuthCallbackMode, error) {
	if !issuerResponseSupported {
		return MCPOAuthCallbackSpecific, nil
	}
	if strings.TrimSpace(metadataIssuer) == "" {
		return "", errors.New("OAuth authorization server advertises issuer support without a metadata issuer")
	}
	return MCPOAuthCallbackIssuerBound, nil
}

// ResolveMCPOAuthCallbackURL mirrors Rust resolve_mcp_oauth_callback_url: it
// resolves the registered callback independently of runtime listener ports.
func ResolveMCPOAuthCallbackURL(serverURL string, callbackURL *string, mode MCPOAuthCallbackMode) (string, error) {
	callback := mcpOAuthDefaultCallbackURL
	if callbackURL != nil && strings.TrimSpace(*callbackURL) != "" {
		callback = strings.TrimSpace(*callbackURL)
	}
	switch mode {
	case MCPOAuthCallbackIssuerBound:
		if _, err := url.Parse(callback); err != nil {
			return "", fmt.Errorf("invalid redirect URI `%s`", callback)
		}
		return callback, nil
	default:
		callbackID, err := MCPOAuthCallbackID(serverURL)
		if err != nil {
			return "", err
		}
		return AppendMCPOAuthCallbackID(callback, callbackID)
	}
}

// ValidateMCPOAuthCallbackRedirect mirrors Rust validate_callback_redirect: a
// callback URL must carry its expected callback ID unless issuer binding is
// active for this authorization server.
func ValidateMCPOAuthCallbackRedirect(redirectURI string, callbackID string, mode MCPOAuthCallbackMode) error {
	parsed, err := url.Parse(strings.TrimSpace(redirectURI))
	if err != nil {
		return err
	}
	if mcpOAuthLastPathSegment(parsed.Path) == strings.TrimSpace(callbackID) {
		return nil
	}
	if mode == MCPOAuthCallbackIssuerBound {
		return nil
	}
	return errors.New("OAuth callback requires its expected callback ID or authorization response issuer support")
}

func mcpOAuthLastPathSegment(path string) string {
	segments := strings.Split(path, "/")
	if len(segments) == 0 {
		return ""
	}
	return segments[len(segments)-1]
}

// insertMCPOAuthListenerPort inserts the active listener port into a portless
// loopback redirect so a registered callback can be reused across processes
// (Rust #40691).
func insertMCPOAuthListenerPort(redirectURL string, listenerPort uint16) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(redirectURL))
	if err != nil {
		return "", err
	}
	if parsed.Port() != "" || parsed.Scheme != "http" {
		return parsed.String(), nil
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return parsed.String(), nil
	}
	parsed.Host = net.JoinHostPort(parsed.Hostname(), strconv.Itoa(int(listenerPort)))
	return parsed.String(), nil
}
