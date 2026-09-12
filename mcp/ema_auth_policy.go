package mcp

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// EMAAuthFailureCode is a sanitized enterprise-auth failure that callers may
// handle without parsing text. Mirrors Rust EmaAuthFailure.
type EMAAuthFailureCode string

const (
	EMAAuthFailureInvalidGrant             EMAAuthFailureCode = "invalid_grant"
	EMAAuthFailureInsufficientUserAuthn    EMAAuthFailureCode = "insufficient_user_authentication"
	EMAAuthFailureReauthenticationRequired EMAAuthFailureCode = "enterprise identity requires authentication"
)

// EMAInvalidGrantSource identifies which exchange produced an invalid_grant.
// Mirrors Rust EmaInvalidGrantSource.
type EMAInvalidGrantSource string

const (
	EMAInvalidGrantSourceEnterpriseIdentity    EMAInvalidGrantSource = "enterprise_identity"
	EMAInvalidGrantSourceResourceAuthorization EMAInvalidGrantSource = "resource_authorization"
)

// EMAAuthFailure mirrors Rust's sanitized EmaAuthFailure. Context carries the
// operation/status text so Error() matches Rust's anyhow display chain.
type EMAAuthFailure struct {
	Code        EMAAuthFailureCode
	GrantSource EMAInvalidGrantSource
	Context     string
}

func (e *EMAAuthFailure) Error() string {
	if e == nil {
		return ""
	}
	if e.Context != "" {
		return e.Context
	}
	switch e.Code {
	case EMAAuthFailureInvalidGrant:
		return "invalid_grant"
	case EMAAuthFailureInsufficientUserAuthn:
		return "insufficient_user_authentication"
	case EMAAuthFailureReauthenticationRequired:
		return "enterprise identity requires authentication"
	default:
		return "enterprise authentication failed"
	}
}

// safeMCPEMAOAuthErrorCode mirrors Rust safe_oauth_error_code: only known OAuth
// codes may reach callers, so provider-controlled text cannot reflect secrets.
func safeMCPEMAOAuthErrorCode(code *string) string {
	if code == nil {
		return "OAuth token request rejected"
	}
	switch *code {
	case "invalid_request",
		"invalid_client",
		"invalid_grant",
		"invalid_scope",
		"invalid_target",
		"unauthorized_client",
		"unsupported_grant_type",
		"access_denied",
		"temporarily_unavailable",
		"server_error",
		"insufficient_user_authentication":
		return *code
	default:
		return "OAuth token request rejected"
	}
}

// validateMCPEMAPublicClientAuth mirrors Rust validate_ema_public_client_auth.
func validateMCPEMAPublicClientAuth(advertisedMethods any, issuerDescription string) error {
	if advertisedMethods == nil {
		return fmt.Errorf("%s does not explicitly advertise public-client token endpoint authentication", issuerDescription)
	}
	methods, ok := mcpEMAArray(advertisedMethods)
	if !ok {
		return fmt.Errorf("%s advertised malformed token endpoint authentication methods", issuerDescription)
	}
	if mcpEMAArrayContainsString(methods, "none") {
		return nil
	}
	return fmt.Errorf("%s does not support public-client token endpoint authentication", issuerDescription)
}

// validateMCPEMAOAuthEndpoint mirrors Rust validate_ema_oauth_endpoint.
func validateMCPEMAOAuthEndpoint(endpoint string, description string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("%s is not a valid URL", description)
	}
	return validateMCPEMACredentialDestination(parsed, description)
}

func validateMCPEMACredentialDestination(parsed *url.URL, description string) error {
	if parsed == nil {
		return fmt.Errorf("%s is not a valid URL", description)
	}
	hostname := parsed.Hostname()
	loopback := strings.EqualFold(hostname, "localhost")
	if ip := net.ParseIP(hostname); ip != nil {
		loopback = ip.IsLoopback()
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopback) {
		return fmt.Errorf("%s must use HTTPS or an HTTP loopback address", description)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%s contains disallowed credentials or a URL fragment", description)
	}
	return nil
}

// ValidateMCPEMAAuthResource mirrors Rust validate_ema_auth_resource: a resource
// indicator must describe the configured MCP origin, query, and path.
func ValidateMCPEMAAuthResource(serverURL string, resource *string) error {
	server, err := url.Parse(serverURL)
	if err != nil || server.Scheme == "" {
		return fmt.Errorf("enterprise MCP server URL is invalid")
	}
	if err := validateMCPEMACredentialDestination(server, "enterprise MCP server URL"); err != nil {
		return err
	}
	if resource == nil || strings.TrimSpace(*resource) == "" {
		return nil
	}
	indicator, err := url.Parse(*resource)
	if err != nil || indicator.Scheme == "" {
		return fmt.Errorf("enterprise MCP resource indicator is invalid")
	}
	if err := validateMCPEMACredentialDestination(indicator, "enterprise MCP resource indicator"); err != nil {
		return err
	}
	if mcpEMAURLOrigin(indicator) != mcpEMAURLOrigin(server) || indicator.RawQuery != server.RawQuery {
		return fmt.Errorf("enterprise MCP resource indicator must match the configured MCP server origin and query")
	}
	resourcePath := strings.TrimRight(indicator.Path, "/")
	serverPath := strings.TrimRight(server.Path, "/")
	if serverPath != resourcePath {
		if !strings.HasPrefix(serverPath, resourcePath) {
			return fmt.Errorf("enterprise MCP resource indicator path must contain the configured MCP server path")
		}
		if suffix := serverPath[len(resourcePath):]; !strings.HasPrefix(suffix, "/") {
			return fmt.Errorf("enterprise MCP resource indicator path must contain the configured MCP server path")
		}
	}
	return nil
}

func mcpEMAURLOrigin(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// advertisedMCPEMACapability mirrors Rust advertised_capability. A nil value
// means the capability list was absent; a non-array value is malformed.
func advertisedMCPEMACapability(value any, expected string, description string) (*bool, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := mcpEMAArray(value)
	if !ok {
		return nil, fmt.Errorf("%s is malformed", description)
	}
	found := mcpEMAArrayContainsString(values, expected)
	return &found, nil
}

// mcpEMAArray reports whether value is a JSON array. Element types are not
// constrained: non-string elements are simply not matched, as in Rust's
// `value.as_str() == Some(expected)`.
func mcpEMAArray(value any) ([]any, bool) {
	values, ok := value.([]any)
	return values, ok
}

func mcpEMAArrayContainsString(values []any, expected string) bool {
	for _, candidate := range values {
		if text, ok := candidate.(string); ok && text == expected {
			return true
		}
	}
	return false
}
