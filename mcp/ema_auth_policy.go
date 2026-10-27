package mcp

import (
	"encoding/json"
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
	EMAAuthFailureIdentityClientRejected   EMAAuthFailureCode = "enterprise identity rejected the OAuth client"
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
	case EMAAuthFailureIdentityClientRejected:
		return "enterprise identity rejected the OAuth client"
	case EMAAuthFailureReauthenticationRequired:
		return "enterprise identity requires authentication"
	default:
		return "enterprise authentication failed"
	}
}

// IsAuthenticationRequired reports whether this failure needs a new sign-in
// rather than an administrator. A resource authorization rejection does not
// invalidate the enterprise login itself (Rust #55691).
func (e *EMAAuthFailure) IsAuthenticationRequired() bool {
	if e == nil {
		return false
	}
	switch e.Code {
	case EMAAuthFailureReauthenticationRequired, EMAAuthFailureIdentityClientRejected:
		return true
	case EMAAuthFailureInvalidGrant, EMAAuthFailureInsufficientUserAuthn:
		return e.GrantSource != EMAInvalidGrantSourceResourceAuthorization
	default:
		return false
	}
}

// EMACredentialKind is the credential source selected for enterprise token
// acquisition or validation (Rust EmaCredentialKind).
type EMACredentialKind string

const (
	EMACredentialKindAccount EMACredentialKind = "account"
	EMACredentialKindKeyring EMACredentialKind = "keyring"
)

// EMATokenSourceError carries only a credential kind and an already sanitized
// auth failure to transport. An unexpected failure deliberately retains
// neither provider text nor its source chain (Rust EmaTokenSourceError).
type EMATokenSourceError struct {
	Credential EMACredentialKind
	// Failure is nil when the exchange failed for a reason that is not a
	// sanitized auth failure.
	Failure *EMAAuthFailure
}

// NewEMATokenSourceError reports a sanitized auth failure for one credential kind.
func NewEMATokenSourceError(credential EMACredentialKind, failure *EMAAuthFailure) *EMATokenSourceError {
	return &EMATokenSourceError{Credential: credential, Failure: failure}
}

// UnexpectedEMATokenSourceError reports an exchange failure that carried no
// sanitized auth failure. Its text never includes provider or transport detail.
func UnexpectedEMATokenSourceError(credential EMACredentialKind) *EMATokenSourceError {
	return &EMATokenSourceError{Credential: credential}
}

// Unwrap exposes the sanitized failure so credential-aware callers can classify
// a wrapped token-source error without parsing guidance text (Rust #55859).
func (e *EMATokenSourceError) Unwrap() error {
	if e == nil || e.Failure == nil {
		return nil
	}
	return e.Failure
}

// Error returns sign-in or administrator guidance, never provider error text.
func (e *EMATokenSourceError) Error() string {
	if e == nil {
		return ""
	}
	failure := e.Failure
	if failure != nil && failure.Code == EMAAuthFailureInsufficientUserAuthn &&
		failure.GrantSource == EMAInvalidGrantSourceResourceAuthorization {
		return "The MCP authorization server requires additional user authentication; contact your administrator"
	}
	if failure != nil && failure.Code == EMAAuthFailureIdentityClientRejected && e.Credential == EMACredentialKindKeyring {
		return "The enterprise IdP rejected the configured MCP OAuth client; contact your administrator"
	}
	if failure != nil && e.Credential == EMACredentialKindAccount {
		switch failure.Code {
		case EMAAuthFailureInvalidGrant, EMAAuthFailureInsufficientUserAuthn:
			if failure.GrantSource == EMAInvalidGrantSourceEnterpriseIdentity {
				return "Complete enterprise sign-in for this MCP server"
			}
		case EMAAuthFailureIdentityClientRejected:
			return "Complete enterprise sign-in for this MCP server"
		case EMAAuthFailureReauthenticationRequired:
			return "Sign in to your Codex account to restore enterprise MCP access"
		}
	}
	if failure != nil && e.Credential == EMACredentialKindKeyring {
		switch failure.Code {
		case EMAAuthFailureInvalidGrant, EMAAuthFailureInsufficientUserAuthn:
			if failure.GrantSource == EMAInvalidGrantSourceEnterpriseIdentity {
				return "Complete enterprise sign-in to restore MCP access"
			}
		case EMAAuthFailureReauthenticationRequired:
			return "Complete enterprise sign-in to restore MCP access"
		}
	}
	if failure != nil && failure.Code == EMAAuthFailureInvalidGrant &&
		failure.GrantSource == EMAInvalidGrantSourceResourceAuthorization {
		return "The MCP authorization server rejected the enterprise grant: invalid_grant"
	}
	return "enterprise MCP token exchange failed"
}

// IsAuthenticationRequired mirrors Rust's credential-aware classification: a
// keyring client rejection needs an administrator, while an account client
// rejection needs a fresh Codex sign-in.
func (e *EMATokenSourceError) IsAuthenticationRequired() bool {
	if e == nil || e.Failure == nil {
		return false
	}
	switch e.Failure.Code {
	case EMAAuthFailureReauthenticationRequired:
		return true
	case EMAAuthFailureInvalidGrant, EMAAuthFailureInsufficientUserAuthn:
		return e.Failure.GrantSource == EMAInvalidGrantSourceEnterpriseIdentity
	case EMAAuthFailureIdentityClientRejected:
		return e.Credential == EMACredentialKindAccount
	default:
		return false
	}
}

// MCPEMARequestNotSentError reports that enterprise policy held a request
// before dispatch (Rust ExecServerError::RequestNotSent). Transport retry must
// not replay it; the caller retries once the policy is applied.
type MCPEMARequestNotSentError struct {
	Reason string
}

func (e *MCPEMARequestNotSentError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return "request not sent: enterprise MCP policy is updating or unavailable"
	}
	return "request not sent: " + e.Reason
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

// advertisedMCPEMAJSONCapability decodes one raw metadata field before
// applying advertisedMCPEMACapability. An absent field yields nil, while a JSON
// null is malformed, as in Rust's `Value::as_array` on null.
func advertisedMCPEMAJSONCapability(raw []byte, expected string, description string) (*bool, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, fmt.Errorf("%s is malformed", description)
	}
	return advertisedMCPEMACapability(value, expected, description)
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
