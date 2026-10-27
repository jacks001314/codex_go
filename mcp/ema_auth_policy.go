package mcp

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	managedconfig "codex_go/config"
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

// mcpEMAClientAuthentication is the client authentication approved for one
// token endpoint. An empty Method is the public-client `none` method. String
// never exposes the secret (Rust #56001).
type mcpEMAClientAuthentication struct {
	Method       managedconfig.MCPEMATokenEndpointAuthMethod
	ClientID     string
	ClientSecret string
}

func (a mcpEMAClientAuthentication) String() string {
	return fmt.Sprintf("mcpEMAClientAuthentication{method:%q client_id:%q client_secret:[REDACTED]}", a.Method, a.ClientID)
}

func (a mcpEMAClientAuthentication) GoString() string {
	return a.String()
}

// resolveMCPEMAClientAuth mirrors Rust resolve_ema_client_auth: a confidential
// registration is used exactly as configured after the server advertises that
// method, while a public registration still requires an advertised `none`.
// advertised is nil when the server published no method list.
func resolveMCPEMAClientAuth(
	advertised []string,
	clientID string,
	clientSecret string,
	method managedconfig.MCPEMATokenEndpointAuthMethod,
	issuerDescription string,
) (mcpEMAClientAuthentication, error) {
	if method != "" && !method.IsValid() {
		return mcpEMAClientAuthentication{}, fmt.Errorf("unknown value %q for %s token endpoint authentication method", method, issuerDescription)
	}
	if clientSecret != "" {
		if strings.TrimSpace(clientSecret) == "" {
			return mcpEMAClientAuthentication{}, fmt.Errorf("%s client secret must not be empty", issuerDescription)
		}
		registered := method
		if registered == "" {
			registered = managedconfig.MCPEMATokenEndpointAuthClientSecretBasic
		}
		// RFC 8414 defaults absent server capabilities to Basic. Capabilities
		// validate the registration; they do not select its authentication method.
		supported := false
		if advertised == nil {
			supported = registered == managedconfig.MCPEMATokenEndpointAuthClientSecretBasic
		} else {
			for _, candidate := range advertised {
				if candidate == string(registered) {
					supported = true
					break
				}
			}
		}
		if !supported {
			return mcpEMAClientAuthentication{}, fmt.Errorf("%s does not support the registered token endpoint authentication method", issuerDescription)
		}
		return mcpEMAClientAuthentication{Method: registered, ClientID: clientID, ClientSecret: clientSecret}, nil
	}
	if method != "" {
		return mcpEMAClientAuthentication{}, fmt.Errorf("%s token endpoint authentication method requires a client secret", issuerDescription)
	}
	if advertised == nil {
		return mcpEMAClientAuthentication{}, fmt.Errorf("%s does not explicitly advertise public-client token endpoint authentication", issuerDescription)
	}
	for _, candidate := range advertised {
		if candidate == "none" {
			return mcpEMAClientAuthentication{ClientID: clientID}, nil
		}
	}
	return mcpEMAClientAuthentication{}, fmt.Errorf("%s does not support public-client token endpoint authentication", issuerDescription)
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
